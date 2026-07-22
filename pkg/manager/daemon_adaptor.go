/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/command"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	metrics "github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
	"github.com/containerd/nydus-snapshotter/pkg/prefetch"
)

const endpointGetBackend string = "/api/v1/daemons/%s/backend"

// defaultDaemonTerminationTimeout is how long we wait for a failed nydusd to
// exit after SIGTERM before escalating to SIGKILL. It is a var so tests can
// shorten it.
var defaultDaemonTerminationTimeout = 5 * time.Second

// Spawn a nydusd daemon to serve the daemon instance.
//
// When returning from `StartDaemon()` with out error:
//   - `d.States.ProcessID` will be set to the pid of the nydusd daemon.
//   - `d.State()` may return any validate state, please call `d.WaitUntilState()` to
//     ensure the daemon has reached specified state.
//   - `d` may have not been inserted into daemonStates and store yet.
func (m *Manager) StartDaemon(d *daemon.Daemon) error {
	cmd, err := m.BuildDaemonCommand(d, "", false)
	if err != nil {
		return errors.Wrapf(err, "create command for daemon %s", d.ID())
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	d.Lock()
	defer d.Unlock()

	d.States.ProcessID = cmd.Process.Pid

	// Profile nydusd daemon CPU usage during its startup.
	if config.GetDaemonProfileCPUDuration() > 0 {
		processState, err := metrics.GetProcessStat(cmd.Process.Pid)
		if err == nil {
			timer := time.NewTimer(time.Duration(config.GetDaemonProfileCPUDuration()) * time.Second)

			go func() {
				<-timer.C
				currentProcessState, err := metrics.GetProcessStat(cmd.Process.Pid)
				if err != nil {
					log.L.WithError(err).Warnf("Failed to get daemon %s process state.", d.ID())
					return
				}
				d.StartupCPUUtilization, err = metrics.CalculateCPUUtilization(processState, currentProcessState)
				if err != nil {
					log.L.WithError(err).Warnf("Calculate CPU utilization error")
				}
			}()
		}
	}

	// Update both states cache and DB
	// TODO: Is it right to commit daemon before nydusd successfully started?
	// And it brings extra latency of accessing DB. Only write daemon record to
	// DB when nydusd is started?
	err = m.UpdateDaemon(d)
	if err != nil {
		// Nothing we can do, just ignore it for now
		log.L.Errorf("Fail to update daemon info (%+v) to DB: %v", d, err)
	}

	// Verify the daemon actually comes up in the background. If it does not,
	// terminate the spawned process so it cannot linger as an orphan that keeps
	// pulling from the registry (see #771). `proc` is captured here so we act on
	// exactly the process we started, regardless of any concurrent pid changes.
	proc := cmd.Process
	go func() {
		if err := daemon.WaitUntilSocketExisted(d.GetAPISock(), d.States.ProcessID); err != nil {
			log.L.Errorf("Nydusd %s probably not started, terminating it: %v", d.ID(), err)
			m.terminateFailedDaemon(d, proc)
			return
		}

		if err = m.SubscribeDaemonEvent(d); err != nil {
			log.L.Errorf("Failed to subscribe nydusd %s events, terminating it: %v", d.ID(), err)
			m.terminateFailedDaemon(d, proc)
			return
		}

		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			// A failover-managed daemon (recover_policy=failover) legitimately
			// stays in INIT/READY while the failover/upgrade flow drives
			// INIT -> TakeOver -> Start after StartDaemon returns. Do not treat
			// that as a failed startup: killing or unsubscribing it here would
			// wreck the takeover. Its cleanup belongs to the failover flow.
			if d.Supervisor != nil {
				log.L.WithError(err).Warnf("daemon %s did not reach RUNNING yet, leaving it to the failover flow", d.ID())
				return
			}
			log.L.WithError(err).Errorf("daemon %s is not managed to reach RUNNING state, terminating it", d.ID())
			// Unsubscribe before killing, otherwise the liveness monitor would
			// observe the kill as a death event and (with recover_policy=restart)
			// immediately respawn what we just decided to reap.
			if uerr := m.UnsubscribeDaemonEvent(d); uerr != nil {
				log.L.WithError(uerr).Warnf("unsubscribe daemon %s after startup failure", d.ID())
			}
			m.terminateFailedDaemon(d, proc)
			return
		}

		collector.NewDaemonEventCollector(types.DaemonStateRunning).Collect()

		if m.CgroupMgr != nil {
			if err := m.CgroupMgr.AddProc(d.States.ProcessID); err != nil {
				log.L.WithError(err).Errorf("add daemon %s to cgroup failed", d.ID())
				return
			}
		}

		d.Lock()
		collector.NewDaemonInfoCollector(&d.Version, 1).Collect()
		d.Unlock()

		d.SendStates()
	}()

	return nil
}

// terminateFailedDaemon best-effort stops a nydusd that failed startup
// verification, so it stops pulling from the registry instead of lingering as
// an orphan. SIGTERM is tried first and escalated to SIGKILL if the process,
// for example one wedged on a slow registry, does not exit in time. The process
// is finally reaped to avoid leaving a zombie. It operates on the captured
// process handle, so it is safe against pid reuse and concurrent teardown.
func (m *Manager) terminateFailedDaemon(d *daemon.Daemon, proc *os.Process) {
	if proc == nil {
		return
	}

	// A failover-managed daemon (recover_policy=failover) legitimately stays in
	// INIT/READY while the failover flow drives INIT -> TakeOver -> Start after
	// StartDaemon returns, so a "not RUNNING yet" verdict here may be a takeover
	// in progress rather than a failed startup. Killing it would destroy the
	// takeover, so leave its cleanup to the failover flow.
	if d.Supervisor != nil {
		log.L.Warnf("nydusd %s (pid %d) failed startup verification, leaving cleanup to the failover flow", d.ID(), proc.Pid)
		return
	}

	// For a shared daemon, if it fails to reach RUNNING, it won't be retained
	// by TryRetainSharedDaemon. We must kill it here so it doesn't linger.
	// But if it is already retained (e.g., this is a spurious timeout or
	// TryRetainSharedDaemon was called concurrently), killing it would drop
	// the active shared daemon. We check IsSharedDaemon() and whether it is
	// the currently active one.
	if d.IsSharedDaemon() && m.isDaemonRetainedAsShared(d) {
		log.L.Warnf("nydusd %s (pid %d) failed startup verification but is already retained as shared daemon, not killing it", d.ID(), proc.Pid)
		return
	}

	log.L.Warnf("terminating nydusd %s (pid %d) that failed to start", d.ID(), proc.Pid)

	done := make(chan struct{})
	go func() {
		_, _ = proc.Wait()
		close(done)
	}()

	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		log.L.WithError(err).Warnf("send SIGTERM to nydusd %s (pid %d)", d.ID(), proc.Pid)
	}

	select {
	case <-done:
	case <-time.After(defaultDaemonTerminationTimeout):
		log.L.Warnf("nydusd %s (pid %d) did not exit after SIGTERM, sending SIGKILL", d.ID(), proc.Pid)
		if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.L.WithError(err).Warnf("send SIGKILL to nydusd %s (pid %d)", d.ID(), proc.Pid)
		}
		<-done
	}
}

// isDaemonRetainedAsShared checks if the daemon is currently retained as the
// active shared daemon in the filesystem manager. This is a callback provided
// by the manager to avoid circular dependencies.
func (m *Manager) isDaemonRetainedAsShared(d *daemon.Daemon) bool {
	if m.IsSharedDaemonRetained != nil {
		return m.IsSharedDaemonRetained(d)
	}
	return false
}

// Build commandline according to nydusd daemon configuration.
func (m *Manager) BuildDaemonCommand(d *daemon.Daemon, bin string, upgrade bool) (*exec.Cmd, error) {
	var cmdOpts []command.Opt
	var imageReference string

	nydusdThreadNum := d.NydusdThreadNum()

	if d.States.FsDriver == config.FsDriverFscache {
		cmdOpts = append(cmdOpts,
			command.WithMode("singleton"),
			command.WithFscacheDriver(m.cacheDir))
		if nydusdThreadNum != 0 {
			cmdOpts = append(cmdOpts, command.WithFscacheThreads(nydusdThreadNum))
		}
	} else {
		cmdOpts = append(cmdOpts, command.WithMode("fuse"), command.WithMountpoint(d.HostMountpoint()))
		if nydusdThreadNum != 0 {
			cmdOpts = append(cmdOpts, command.WithThreadNum(nydusdThreadNum))
		}

		switch {
		case d.IsSharedDaemon():
			break
		case !d.IsSharedDaemon():
			rafs := d.RafsCache.Head()
			if rafs == nil {
				return nil, errors.Wrapf(errdefs.ErrNotFound, "daemon %s no rafs instance associated", d.ID())
			}

			imageReference = rafs.ImageID

			bootstrap, err := rafs.BootstrapFile()
			if err != nil {
				return nil, errors.Wrapf(err, "locate bootstrap %s", bootstrap)
			}

			cmdOpts = append(cmdOpts,
				command.WithConfig(d.ConfigFile("")),
				command.WithBootstrap(bootstrap),
			)
			if config.IsBackendSourceEnabled() {
				configAPIPath := fmt.Sprintf(endpointGetBackend, d.States.ID)
				cmdOpts = append(cmdOpts,
					command.WithBackendSource(config.SystemControllerAddress()+configAPIPath),
				)
			}
		default:
			return nil, errors.Errorf("invalid daemon mode %s ", d.States.DaemonMode)
		}
	}

	if d.Supervisor != nil {
		cmdOpts = append(cmdOpts,
			command.WithSupervisor(d.Supervisor.Sock()),
			command.WithID(d.ID()))
	}

	if imageReference != "" {
		prefetchfiles := prefetch.Pm.GetPrefetchInfo(imageReference)
		if prefetchfiles != "" {
			cmdOpts = append(cmdOpts, command.WithPrefetchFiles(prefetchfiles))
			prefetch.Pm.DeleteFromPrefetchMap(imageReference)
		}
	}

	cmdOpts = append(cmdOpts,
		command.WithLogLevel(d.States.LogLevel),
		command.WithAPISock(d.GetAPISock()))

	if d.States.LogRotationSize > 0 {
		cmdOpts = append(cmdOpts, command.WithLogRotationSize(d.States.LogRotationSize))
	}

	if upgrade {
		cmdOpts = append(cmdOpts, command.WithUpgrade())
	}

	if !d.States.LogToStdout {
		cmdOpts = append(cmdOpts, command.WithLogFile(d.LogFile()))
	}

	if d.States.FsDriver == config.FsDriverFusedev {
		cmdOpts = append(cmdOpts, command.WithFailoverPolicy(d.States.FailoverPolicy))
	}

	args, err := command.BuildCommand(cmdOpts)
	if err != nil {
		return nil, err
	}

	var nydusdPath string
	if bin != "" {
		nydusdPath = bin
	} else {
		nydusdPath = m.NydusdBinaryPath
	}

	log.L.Infof("nydusd command: %s %s", nydusdPath, strings.Join(args, " "))

	cmd := exec.Command(nydusdPath, args...)

	// nydusd standard output and standard error rather than its logs are
	// always redirected to snapshotter's respectively
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}
