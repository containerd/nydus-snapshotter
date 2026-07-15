/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/pkg/errors"
)

var (
	defaultDaemonTerminationTimeout = 5 * time.Second
	processExitPollInterval         = 100 * time.Millisecond
)

func (m *Manager) SubscribeDaemonEvent(d *daemon.Daemon) error {
	pid := daemonProcessID(d)
	if err := m.monitor.Subscribe(d.ID(), pid, d.GetAPISock(), m.LivenessNotifier); err != nil {
		log.L.Errorf("Nydusd %s probably not started", d.ID())
		return errors.Wrapf(err, "subscribe daemon %s", d.ID())
	}
	return nil
}

func (m *Manager) UnsubscribeDaemonEvent(d *daemon.Daemon) error {
	// Starting a new nydusd will re-subscribe
	if err := m.monitor.Unsubscribe(d.ID()); err != nil {
		log.L.Warnf("fail to unsubscribe daemon %s, %v", d.ID(), err)
		return errors.Wrapf(err, "unsubscribe daemon %s", d.ID())
	}
	return nil
}

func (m *Manager) handleDaemonDeathEvent() {
	for ev := range m.LivenessNotifier {
		log.L.Warnf("Daemon %s died! socket path %s", ev.daemonID, ev.path)

		d := m.GetByDaemonID(ev.daemonID)
		if d == nil {
			log.L.Warnf("Daemon %s was not found, may have been removed", ev.daemonID)
			continue
		}
		currentPID := daemonProcessID(d)
		if ev.processID != currentPID {
			log.L.Warnf(
				"Ignore stale death event for daemon %s: event pid %d, current pid %d",
				ev.daemonID, ev.processID, currentPID,
			)
			continue
		}

		if m.isRecoveryInFlight(ev.daemonID) {
			log.L.Warnf("Recovery already in progress for daemon %s, skipping duplicate death event", ev.daemonID)
			continue
		}

		d.Lock()
		collector.NewDaemonInfoCollector(&d.Version, -1).Collect()
		d.Unlock()

		d.ResetState()

		switch m.RecoverPolicy {
		case config.RecoverPolicyRestart:
			log.L.Infof("Restart daemon %s", ev.daemonID)
			go m.doDaemonRestart(d)
		case config.RecoverPolicyFailover:
			log.L.Infof("Do failover for daemon %s", ev.daemonID)
			go m.doDaemonFailover(d)
		default:
			m.recoveryInFlight.Delete(ev.daemonID)
		}
	}
}

// isRecoveryInFlight marks daemonID as recovering and reports whether a
// recovery was already in progress, so duplicate death events are ignored.
func (m *Manager) isRecoveryInFlight(daemonID string) bool {
	_, loaded := m.recoveryInFlight.LoadOrStore(daemonID, struct{}{})
	return loaded
}

func daemonProcessID(d *daemon.Daemon) int {
	d.Lock()
	defer d.Unlock()
	return d.Pid()
}

func (m *Manager) doDaemonFailover(d *daemon.Daemon) {
	defer m.recoveryInFlight.Delete(d.ID())

	if err := m.terminateDaemonProcess(d, "failover"); err != nil {
		log.L.WithError(err).Errorf("abort failover because old daemon %s was not terminated", d.ID())
		return
	}

	if err := m.UnsubscribeDaemonEvent(d); err != nil {
		log.L.Warnf("fail to unsubscribe daemon %s, %v", d.ID(), err)
	}

	su := m.SupervisorSet.GetSupervisor(d.ID())
	if err := su.SendStatesTimeout(time.Second * 10); err != nil {
		log.L.Errorf("Send states error, %s", err)
		return
	}

	// Failover nydusd still depends on the old supervisor

	if err := m.StartDaemon(d); err != nil {
		log.L.Errorf("fail to start daemon %s when recovering", d.ID())
		return
	}

	if err := d.WaitUntilState(types.DaemonStateInit); err != nil {
		log.L.WithError(err).Errorf("daemon didn't reach state %s,", types.DaemonStateInit)
		return
	}

	if err := d.TakeOver(); err != nil {
		log.L.Errorf("fail to takeover, %s", err)
		return
	}

	if err := d.Start(); err != nil {
		log.L.Errorf("fail to start service, %s", err)
		return
	}
}

func (m *Manager) doDaemonRestart(d *daemon.Daemon) {
	defer m.recoveryInFlight.Delete(d.ID())

	if err := m.terminateDaemonProcess(d, "restart"); err != nil {
		log.L.WithError(err).Errorf("abort restart because old daemon %s was not terminated", d.ID())
		return
	}

	if err := m.UnsubscribeDaemonEvent(d); err != nil {
		log.L.Warnf("fails to unsubscribe daemon %s, %v", d.ID(), err)
	}

	d.ClearVestige()
	if err := m.StartDaemon(d); err != nil {
		log.L.Errorf("fails to start daemon %s when recovering", d.ID())
		return
	}

	// Mount rafs instance by http API
	instances := d.RafsCache.List()
	for _, r := range instances {
		// For dedicated nydusd daemon, Rafs has already been mounted during starting nydusd
		if d.HostMountpoint() == r.GetMountpoint() {
			break
		}

		if err := d.SharedMount(r); err != nil {
			log.L.Warnf("Failed to mount rafs instance, %v", err)
		}
	}
}

// Make sure the old nydusd process is gone before starting a new one.
// If the process is still alive — the death event may be a false positive
// (e.g. API socket closed while the process hangs) — kill it with SIGTERM,
// escalating to SIGKILL after a timeout. If it has already exited, just reap
// it like the original recovery flow did, to avoid leaving a zombie behind.
func (m *Manager) terminateDaemonProcess(d *daemon.Daemon, reason string) error {
	if d == nil || d.Pid() <= 0 {
		return nil
	}

	// On Linux, os.FindProcess uses a pidfd when available, pinning this
	// handle to the specific process before the cmdline identity check below,
	// so a later signal can never reach a reused pid.
	p, err := os.FindProcess(d.Pid())
	if err != nil {
		return errors.Wrapf(err, "find daemon %s (pid %d) before %s", d.ID(), d.Pid(), reason)
	}
	defer func() {
		_ = p.Release()
	}()

	state, err := inspectDaemonProcess(d)
	if err != nil {
		return errors.Wrapf(err, "inspect daemon %s (pid %d) before %s", d.ID(), d.Pid(), reason)
	}

	switch state {
	case daemonProcessAlive:
		// The daemon is still running; continue with the termination path below.
	case daemonProcessGone, daemonProcessReused:
		return nil
	case daemonProcessZombie:
		// The process has already exited; reaping the zombie is instantaneous.
		return d.Wait()
	}

	if err := p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Wrapf(err, "send SIGTERM to daemon %s (pid %d) before %s", d.ID(), d.Pid(), reason)
	}

	exited, err := waitUntilDaemonExited(d, defaultDaemonTerminationTimeout)
	if err == nil {
		if exited == daemonProcessZombie {
			return d.Wait()
		}
		return nil
	}

	log.L.Warnf("daemon %s (pid %d) did not exit after SIGTERM before %s, sending SIGKILL", d.ID(), d.Pid(), reason)
	if err := p.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Wrapf(err, "send SIGKILL to daemon %s (pid %d) before %s", d.ID(), d.Pid(), reason)
	}

	exited, err = waitUntilDaemonExited(d, defaultDaemonTerminationTimeout)
	if err != nil {
		return errors.Wrapf(err, "daemon %s (pid %d) still alive after SIGKILL before %s", d.ID(), d.Pid(), reason)
	}
	if exited == daemonProcessZombie {
		return d.Wait()
	}
	return nil
}

type daemonProcessState int

const (
	daemonProcessAlive daemonProcessState = iota
	daemonProcessZombie
	daemonProcessGone
	daemonProcessReused
)

// inspectDaemonProcess reads /proc/<pid>/cmdline and classifies the process.
func inspectDaemonProcess(d *daemon.Daemon) (daemonProcessState, error) {
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", d.Pid()))
	return classifyProcessCmdline(cmdline, err, d.GetAPISock())
}

// classifyProcessCmdline distinguishes an exited process from an inspection
// failure. A matching --apisock identifies the current nydusd; an empty
// cmdline means zombie, and a different cmdline means the pid was reused.
func classifyProcessCmdline(cmdline []byte, readErr error, apisock string) (daemonProcessState, error) {
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return daemonProcessGone, nil
		}
		return daemonProcessGone, readErr
	}
	if len(cmdline) == 0 {
		return daemonProcessZombie, nil
	}

	args := strings.Split(string(cmdline), "\x00")
	if !containsArgPair(args, "--apisock", apisock) {
		return daemonProcessReused, nil
	}

	return daemonProcessAlive, nil
}

func waitUntilDaemonExited(d *daemon.Daemon, timeout time.Duration) (daemonProcessState, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(processExitPollInterval)
	defer ticker.Stop()

	for {
		state, err := inspectDaemonProcess(d)
		if err == nil && state != daemonProcessAlive {
			return state, nil
		}

		select {
		case <-ticker.C:
		case <-deadline.C:
			if err != nil {
				return daemonProcessAlive, errors.Wrap(err, "inspect process")
			}
			return daemonProcessAlive, errors.Errorf("process did not exit within %s", timeout)
		}
	}
}

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

// Provide minimal parameters since most of it can be recovered by nydusd states.
// Create a new daemon in Manger to take over the service.
func (m *Manager) DoDaemonUpgrade(d *daemon.Daemon, nydusdPath string, manager *Manager) (*daemon.Daemon, error) {
	supervisor := d.Supervisor

	newDaemon := &daemon.Daemon{
		States:     d.States,
		Supervisor: supervisor,
	}
	newDaemon.CloneRafsInstances(d)

	s := path.Base(d.GetAPISock())
	next, err := buildNextAPISocket(s)
	if err != nil {
		return nil, err
	}

	upgradingSocket := path.Join(path.Dir(d.GetAPISock()), next)
	newDaemon.States.APISocket = upgradingSocket

	cmd, err := manager.BuildDaemonCommand(newDaemon, nydusdPath, true)
	if err != nil {
		return nil, err
	}

	if err := supervisor.SendStatesTimeout(time.Second * 10); err != nil {
		return nil, errors.Wrap(err, "Send states")
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(err, "start process")
	}

	newDaemon.States.ProcessID = cmd.Process.Pid

	if err := newDaemon.WaitUntilState(types.DaemonStateInit); err != nil {
		return nil, errors.Wrap(err, "wait until init state")
	}

	if err := newDaemon.TakeOver(); err != nil {
		return nil, errors.Wrap(err, "take over resources")
	}

	if err := newDaemon.WaitUntilState(types.DaemonStateReady); err != nil {
		return nil, errors.Wrap(err, "wait unit ready state")
	}

	if err := manager.UnsubscribeDaemonEvent(d); err != nil {
		return nil, errors.Wrap(err, "unsubscribe daemon event")
	}

	// Let the older daemon exit without umount
	if err := d.Exit(); err != nil {
		return nil, errors.Wrap(err, "old daemon exits")
	}

	if err := newDaemon.Start(); err != nil {
		return nil, errors.Wrap(err, "start file system service")
	}

	if err := manager.SubscribeDaemonEvent(newDaemon); err != nil {
		return nil, errors.Wrap(err, "subscribe new daemon event")
	}

	if err := newDaemon.WaitUntilState(types.DaemonStateRunning); err != nil {
		return nil, errors.Wrapf(err, "wait for daemon %s", d.ID())
	}
	if err := newDaemon.RecoverRafsInstances(); err != nil {
		return nil, errors.Wrapf(err, "recover mounts for daemon %s", d.ID())
	}

	log.L.Infof("Started service of upgraded daemon on socket %s", newDaemon.GetAPISock())

	if err := manager.UpdateDaemon(newDaemon); err != nil {
		return nil, err
	}

	log.L.Infof("Upgraded daemon success on socket %s", newDaemon.GetAPISock())
	return newDaemon, err
}

// Name next api socket path based on currently api socket path listened on.
// The principle is to add a suffix number to api[0-9]+.sock
func buildNextAPISocket(cur string) (string, error) {
	n := strings.Split(cur, ".")
	if len(n) != 2 {
		return "", errors.Errorf("invalid api socket path format: %s", cur)
	}
	r := regexp.MustCompile(`[0-9]+`)
	m := r.Find([]byte(n[0]))
	var num int
	if m == nil {
		num = 1
	} else {
		var err error
		num, err = strconv.Atoi(string(m))
		if err != nil {
			return "", err
		}
		num++
	}

	nextSocket := fmt.Sprintf("api%d.sock", num)
	return nextSocket, nil
}
