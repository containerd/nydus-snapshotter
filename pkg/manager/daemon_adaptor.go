/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	stderrors "errors"
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
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

const endpointGetBackend string = "/api/v1/daemons/%s/backend"

var (
	errSystemdMainPIDNotReady = stderrors.New("systemd MainPID is still 0")

	systemdShowMainPID = func(serviceUnit string) ([]byte, error) {
		cmd := exec.Command("systemctl", "show", "--property=MainPID", strings.TrimSpace(serviceUnit))
		return cmd.CombinedOutput()
	}

	systemdMainPIDPollAttempts uint = 100
	systemdMainPIDPollDelay         = 100 * time.Millisecond
)

// StartDaemonProcess spawns a nydusd process and returns the daemon pid.
// When delegation is enabled, the returned pid is the actual host-side nydusd pid,
// not the transient systemd-run pid.
func (m *Manager) StartDaemonProcess(d *daemon.Daemon, bin string, upgrade bool) (int, error) {
	var nydusdPid int
	if m.delegateNydusd {
		serviceUnit := ""
		if upgrade {
			serviceUnit = buildUpgradeServiceUnitName(d.ID())
		}
		if err := executeInMntNamespace("/proc/1/ns/mnt", func() error {
			var err error
			cmd, err := m.BuildDaemonCommand(d, bin, upgrade, serviceUnit)
			if err != nil {
				return errors.Wrapf(err, "create command for daemon %s", d.ID())
			}

			o, err := cmd.CombinedOutput()
			if err != nil {
				return errors.Wrapf(err, "start delegatee %s", d.ID())
			}

			if serviceUnit != "" {
				nydusdPid, err = parseNydusdPidFromServiceUnit(serviceUnit)
			} else {
				nydusdPid, err = parseNydusdPid(string(o))
			}
			if err != nil {
				return errors.Wrapf(err, "parse nydusd pid for delegatee %s", d.ID())
			}

			log.L.Infof("Delegatee nydusd %s started with pid %d", d.ID(), nydusdPid)
			return nil
		}); err != nil {
			return 0, errors.Wrapf(err, "start daemon %s", d.ID())
		}
	} else {
		var err error
		cmd, err := m.BuildDaemonCommand(d, bin, upgrade, "")
		if err != nil {
			return 0, errors.Wrapf(err, "create command for daemon %s", d.ID())
		}

		if err := cmd.Start(); err != nil {
			return 0, err
		}

		nydusdPid = cmd.Process.Pid
	}

	return nydusdPid, nil
}

// Spawn a nydusd daemon to serve the daemon instance.
//
// When returning from `StartDaemon()` with out error:
//   - `d.States.ProcessID` will be set to the pid of the nydusd daemon.
//   - `d.State()` may return any validate state, please call `d.WaitUntilState()` to
//     ensure the daemon has reached specified state.
//   - `d` may have not been inserted into daemonStates and store yet.
func (m *Manager) StartDaemon(d *daemon.Daemon) error {
	nydusdPid, err := m.StartDaemonProcess(d, "", false)
	if err != nil {
		return err
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
func (m *Manager) BuildDaemonCommand(d *daemon.Daemon, bin string, upgrade bool, serviceUnit string) (*exec.Cmd, error) {
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

	var cmd *exec.Cmd
	log.L.Infof("nydusd command: %s %s", nydusdPath, strings.Join(args, " "))
	if m.delegateNydusd {
		delegateeCmdFlags := []string{"--description", "nydusd"}
		if serviceUnit != "" {
			delegateeCmdFlags = append(delegateeCmdFlags, "--unit", serviceUnit)
		}
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

func buildUpgradeServiceUnitName(daemonID string) string {
	return fmt.Sprintf("nydusd-upgrade-%s-%x.service", daemonID, time.Now().UnixNano())
}

// executeInMntNamespace runs fn in the specified mount namespace on a dedicated
// OS thread and restores the original namespace before returning.
func executeInMntNamespace(mntNamespacePath string, fn func() error) (retErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	currentNSPath := fmt.Sprintf("/proc/self/task/%d/ns/mnt", unix.Gettid())
	currentNS, err := os.Open(currentNSPath)
	if err != nil {
		return errors.Wrapf(err, "failed to open current mnt namespace %q", currentNSPath)
	}
	defer currentNS.Close()

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

	defer func() {
		if err := unix.Setns(int(currentNS.Fd()), unix.CLONE_NEWNS); err != nil {
			restoreErr := errors.Wrap(err, "failed to restore original mnt namespace")
			if retErr != nil {
				retErr = stderrors.Join(retErr, restoreErr)
			} else {
				retErr = restoreErr
			}
		}
	}()

	if err := fn(); err != nil {
		log.L.WithError(err).Errorf("failed to execute in the mnt namespace %s", mntNamespacePath)
		return err
	}

	return nil
}

func parseNydusdPid(data string) (int, error) {
	// Parse the output of `systemd-run` to get the transient service unit name,
	// which can then be used to query the nydusd PID in the host pid namespace.
	// The new-born nydusd is not a direct child of nydus-snapshotter so we can't
	// get its PID directly from the exec command.
	serviceUnit, err := parseSystemdRunServiceUnit(data)
	if err != nil {
		return 0, err
	}

	return parseNydusdPidFromServiceUnit(serviceUnit)
}

func parseSystemdRunServiceUnit(data string) (string, error) {
	const runningAsUnitPrefix = "Running as unit:"

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, runningAsUnitPrefix) {
			continue
		}

		serviceUnit := strings.TrimSpace(strings.TrimPrefix(line, runningAsUnitPrefix))
		if serviceUnit == "" {
			return "", errors.Errorf("empty service unit in systemd-run output %q", strings.TrimSpace(data))
		}

		return serviceUnit, nil
	}

	return "", errors.Errorf("failed to find service unit in systemd-run output %q", strings.TrimSpace(data))
}

func parseNydusdPidFromServiceUnit(serviceUnit string) (int, error) {
	serviceUnit = strings.TrimSpace(serviceUnit)

	var nydusdPid int
	err := retry.Do(func() error {
		// systemctl show --property=MainPID run-rc28cf5fdc45c497cbe6736aea1e2701e.service
		// MainPID=1037947
		o, err := systemdShowMainPID(serviceUnit)
		if err != nil {
			return retry.Unrecoverable(errors.Wrapf(err, "get nydusd MainPID from delegatee"))
		}

		nydusdPid, err = parseSystemdMainPID(string(o))
		if err != nil {
			if stderrors.Is(err, errSystemdMainPIDNotReady) {
				return err
			}
			return retry.Unrecoverable(err)
		}

		return nil
	},
		retry.Attempts(systemdMainPIDPollAttempts),
		retry.LastErrorOnly(true),
		retry.Delay(systemdMainPIDPollDelay),
		retry.DelayType(retry.FixedDelay),
	)
	if err != nil {
		if stderrors.Is(err, errSystemdMainPIDNotReady) {
			return 0, errors.Errorf(
				"timed out waiting for positive MainPID for delegatee %s after %s",
				serviceUnit,
				time.Duration(systemdMainPIDPollAttempts-1)*systemdMainPIDPollDelay,
			)
		}
		return 0, err
	}

	return nydusdPid, nil
}

func parseSystemdMainPID(data string) (int, error) {
	const mainPIDPrefix = "MainPID="

	output := strings.TrimSpace(data)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, mainPIDPrefix) {
			continue
		}

		pidValue := strings.TrimSpace(strings.TrimPrefix(line, mainPIDPrefix))
		if pidValue == "" {
			return 0, errors.Errorf("empty MainPID in systemctl show output %q", output)
		}

		nydusdPid, err := strconv.Atoi(pidValue)
		if err != nil {
			return 0, errors.Wrapf(err, "parse nydusd pid %q from systemctl show", pidValue)
		}
		if nydusdPid == 0 {
			return 0, errSystemdMainPIDNotReady
		}
		if nydusdPid < 0 {
			return 0, errors.Errorf("unexpected MainPID %q from systemctl show", pidValue)
		}

		return nydusdPid, nil
	}

	return 0, errors.Errorf("failed to find MainPID in systemctl show output %q", output)
}
