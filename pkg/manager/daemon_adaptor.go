/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/containerd/containerd/log"
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
			rafs := d.Instances.Head()
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

	log.L.Infof("nydusd command: %s %s", nydusdPath, strings.Join(args, " "))

	cmd := exec.Command(nydusdPath, args...)

	// nydusd standard output and standard error rather than its logs are
	// always redirected to snapshotter's respectively
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}
