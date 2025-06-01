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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"

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

// Spawn a nydusd daemon to serve the daemon instance.
//
// When returning from `StartDaemon()` with out error:
//   - `d.States.ProcessID` will be set to the pid of the nydusd daemon.
//   - `d.State()` may return any validate state, please call `d.WaitUntilState()` to
//     ensure the daemon has reached specified state.
//   - `d` may have not been inserted into daemonStates and store yet.
func (m *Manager) StartDaemon(d *daemon.Daemon) error {
	var nydusdPid int
	if m.delegateNydusd {
		if err := executeInMntNamespace("/proc/1/ns/mnt", func() error {
			var err error
			cmd, err := m.BuildDaemonCommand(d, "", false)
			if err != nil {
				return errors.Wrapf(err, "create command for daemon %s", d.ID())
			}

			o, err := cmd.CombinedOutput()
			if err != nil {
				return errors.Wrapf(err, "start delegatee %s", d.ID())
			}

			nydusdPid, err = parseNydusdPid(string(o))
			if err != nil {
				return errors.Wrapf(err, "parse nydusd pid for delegatee %s", d.ID())
			}

			log.L.Infof("Delegatee nydusd %s started with pid %d", d.ID(), nydusdPid)
			return nil
		}); err != nil {
			return errors.Wrapf(err, "start daemon %s", d.ID())
		}
	} else {
		var err error
		cmd, err := m.BuildDaemonCommand(d, "", false)
		if err != nil {
			return errors.Wrapf(err, "create command for daemon %s", d.ID())
		}

		if err := cmd.Start(); err != nil {
			return err
		}

		nydusdPid = cmd.Process.Pid
	}

	d.Lock()
	defer d.Unlock()

	d.States.ProcessID = nydusdPid

	// Profile nydusd daemon CPU usage during its startup.
	if config.GetDaemonProfileCPUDuration() > 0 {
		processState, err := metrics.GetProcessStat(nydusdPid)
		if err == nil {
			timer := time.NewTimer(time.Duration(config.GetDaemonProfileCPUDuration()) * time.Second)

			go func() {
				<-timer.C
				currentProcessState, err := metrics.GetProcessStat(nydusdPid)
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
	err := m.UpdateDaemon(d)
	if err != nil {
		// Nothing we can do, just ignore it for now
		log.L.Errorf("Fail to update daemon info (%+v) to DB: %v", d, err)
	}

	// If nydusd fails startup, manager can't subscribe its death event.
	// So we can ignore the subscribing error.
	go func() {
		if err := daemon.WaitUntilSocketExisted(d.GetAPISock(), d.States.ProcessID); err != nil {
			// FIXME: Should clean the daemon record in DB if the nydusd fails starting
			log.L.Errorf("Nydusd %s probably not started", d.ID())
			return
		}

		if err = m.SubscribeDaemonEvent(d); err != nil {
			log.L.Errorf("Nydusd %s probably not started", d.ID())
			return
		}

		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			log.L.WithError(err).Errorf("daemon %s is not managed to reach RUNNING state", d.ID())
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

	var cmd *exec.Cmd
	log.L.Infof("nydusd command: %s %s", nydusdPath, strings.Join(args, " "))
	if m.delegateNydusd {
		delegateeCmdFlags := []string{"--description", "nydusd"}
		args = append([]string{nydusdPath}, args...)
		args = append(delegateeCmdFlags, args...)
		cmd = exec.Command("systemd-run", args...)
	} else {
		cmd = exec.Command(nydusdPath, args...)
		// nydusd's standard output and standard error together with its logs are
		// always redirected to snapshotter.
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	return cmd, nil
}

// executeInMntNamespace runs a function in the specified namespace and ensures thread isolation
func executeInMntNamespace(mntNamespacePath string, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Detach from the shared fs of the rest of the Go process in order to
	// be able to CLONE_NEWNS.
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return errors.Wrap(err, "failed to unshare filesystem namespace")
	}

	targetNS, err := os.Open(mntNamespacePath)
	if err != nil {
		return errors.Wrapf(err, "failed to open target mnt namespace %q", mntNamespacePath)
	}
	defer targetNS.Close()

	if err := unix.Setns(int(targetNS.Fd()), unix.CLONE_NEWNS); err != nil {
		return errors.Wrapf(err, "failed to enter the namespace %q", mntNamespacePath)
	}

	if err := fn(); err != nil {
		log.L.WithError(err).Errorf("failed to execute in the mnt namespace %s", mntNamespacePath)
		return err
	}

	return err
}

func parseNydusdPid(data string) (int, error) {
	// Parse the output of `systemd-run` command, which looks like:
	// Running as unit: run-rc28cf5fdc45c497cbe6736aea1e2701e.service
	// So we can get the service unit name from it, which can be used to query the nydusd PID in the host pid namespace.
	// The new-born nydusd is not a direct child of nydus-snapshotter we we can't get its PID directly from the exec command.
	output := strings.TrimSpace(data)
	serviceUnit := strings.TrimPrefix(output, "Running as unit:")

	// systemctl show --property=MainPID run-rc28cf5fdc45c497cbe6736aea1e2701e.service
	// MainPID=1037947
	cmd := exec.Command("systemctl", "show", "--property=MainPID", strings.TrimSpace(serviceUnit))
	o, err := cmd.CombinedOutput()
	if err != nil {
		return 0, errors.Wrapf(err, "get nydusd MainPID from delegatee")
	}
	output = strings.TrimSpace(string(o))
	tokens := strings.Split(output, "=")
	if len(tokens) != 2 {
		return 0, errors.Errorf("unexpected output %q from systemctl show", output)
	}

	if tokens[0] != "MainPID" {
		return 0, errors.Errorf("unexpected property %q from systemctl show", tokens[0])
	}

	nydusdPid, err := strconv.Atoi(tokens[1])
	if err != nil {
		return 0, errors.Wrapf(err, "parse nydusd pid %q from systemctl show", tokens[1])
	}

	return nydusdPid, nil
}
