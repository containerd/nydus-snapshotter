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

	"github.com/pkg/errors"

	"github.com/containerd/containerd/log"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/command"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	metrics "github.com/containerd/nydus-snapshotter/pkg/metrics/tool"
)

// Fork the nydusd daemon with the process PID decided
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

	processState, err := metrics.GetProcessStat(cmd.Process.Pid)
	if err == nil {
		// TODO: The measuring duration should be capable of configuring when nydus-snapshotter's own config file is GA.
		timer := time.NewTimer(6 * time.Second)

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
		if err := daemon.WaitUntilSocketExisted(d.GetAPISock()); err != nil {
			log.L.Errorf("Nydusd %s probably not started", d.ID())
			return
		}

		// TODO: It's better to subscribe death event when snapshotter
		// has set daemon's state to RUNNING or READY.
		if err := m.monitor.Subscribe(d.ID(), d.GetAPISock(), m.LivenessNotifier); err != nil {
			log.L.Errorf("Nydusd %s probably not started", d.ID())
			return
		}

		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			log.L.WithError(err).Errorf("daemon %s is not managed to reach RUNNING state", d.ID())
			return
		}
		daemonInfo, err := d.GetDaemonInfo()
		if err != nil {
			log.L.WithError(err).Errorf("failed to get daemon %s information", d.ID())
		}
		d.Lock()
		d.Version = daemonInfo.DaemonVersion()
		d.Unlock()
		collector.NewDaemonInfoCollector(&d.Version, 1).Collect()

		if d.Supervisor == nil {
			return
		}

		su := d.Supervisor
		err = su.FetchDaemonStates(func() error {
			if err := d.SendStates(); err != nil {
				return errors.Wrapf(err, "send daemon %s states", d.ID())
			}
			return nil
		})
		if err != nil {
			log.L.Errorf("send states")
			return
		}
	}()

	return nil
}

// Build a daemon command which will be started to fork a new nydusd process later
// according to previously setup daemon object.
func (m *Manager) BuildDaemonCommand(d *daemon.Daemon, bin string, upgrade bool) (*exec.Cmd, error) {
	var cmdOpts []command.Opt

	nydusdThreadNum := d.NydusdThreadNum()

	if d.States.FsDriver == config.FsDriverFscache {
		cmdOpts = append(cmdOpts,
			command.WithMode("singleton"),
			command.WithFscacheDriver(m.cacheDir))

		if nydusdThreadNum != 0 {
			cmdOpts = append(cmdOpts, command.WithFscacheThreads(nydusdThreadNum))
		}

	} else {
		cmdOpts = append(cmdOpts, command.WithMode("fuse"))

		if nydusdThreadNum != 0 {
			cmdOpts = append(cmdOpts, command.WithThreadNum(nydusdThreadNum))
		}

		switch {
		case !m.IsSharedDaemon():
			rafs := d.Instances.Head()
			if rafs == nil {
				return nil, errors.Wrapf(errdefs.ErrNotFound, "daemon %s no rafs instance associated", d.ID())
			}
			bootstrap, err := rafs.BootstrapFile()
			if err != nil {
				return nil, errors.Wrapf(err, "locate bootstrap %s", bootstrap)
			}

			cmdOpts = append(cmdOpts,
				command.WithConfig(d.ConfigFile("")),
				command.WithBootstrap(bootstrap),
				command.WithMountpoint(d.HostMountpoint()))

		case m.IsSharedDaemon():
			cmdOpts = append(cmdOpts, command.WithMountpoint(d.HostMountpoint()))
		default:
			return nil, errors.Errorf("invalid daemon mode %s ", m.daemonMode)
		}
	}

	if d.Supervisor != nil {
		cmdOpts = append(cmdOpts,
			command.WithSupervisor(d.Supervisor.Sock()),
			command.WithID(d.ID()))
	}

	cmdOpts = append(cmdOpts,
		command.WithLogLevel(d.States.LogLevel),
		command.WithAPISock(d.GetAPISock()))

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
	if bin == "" {
		nydusdPath = m.NydusdBinaryPath
	} else {
		nydusdPath = bin
	}

	log.L.Infof("nydusd command: %s %s", nydusdPath, strings.Join(args, " "))

	cmd := exec.Command(nydusdPath, args...)

	// nydusd standard output and standard error rather than its logs are
	// always redirected to snapshotter's respectively
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd, nil
}
