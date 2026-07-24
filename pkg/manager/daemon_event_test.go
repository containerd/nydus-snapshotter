/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManagerForEvents(policy config.DaemonRecoverPolicy) *Manager {
	return &Manager{
		daemonCache:      newDaemonCache(),
		LivenessNotifier: make(chan deathEvent, 10),
		RecoverPolicy:    policy,
	}
}

func TestHandleDaemonDeathEvent_DedupSkipsRecovery(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyNone)

	d := &daemon.Daemon{States: daemon.ConfigState{ID: "d1"}}
	mgr.daemonCache.Add(d)

	// Simulate recovery already in progress.
	mgr.recoveryInFlight.Store("d1", struct{}{})

	mgr.LivenessNotifier <- deathEvent{daemonID: "d1", path: "/test.sock"}
	close(mgr.LivenessNotifier)

	mgr.handleDaemonDeathEvent()

	// The event was skipped: state should NOT have been reset to Unknown.
	assert.NotEqual(t, types.DaemonStateUnknown, d.State(),
		"duplicate event should not reset daemon state")

	// The pre-existing flag should still be present (not deleted by the skipped event).
	_, loaded := mgr.recoveryInFlight.Load("d1")
	assert.True(t, loaded, "in-flight flag should remain for the active recovery")
}

func TestHandleDaemonDeathEvent_FlagClearedAfterRecovery(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyNone)

	d := &daemon.Daemon{States: daemon.ConfigState{ID: "d1"}}
	mgr.daemonCache.Add(d)

	mgr.LivenessNotifier <- deathEvent{daemonID: "d1", path: "/test.sock"}
	close(mgr.LivenessNotifier)

	mgr.handleDaemonDeathEvent()

	// With RecoverPolicyNone, the default branch deletes the flag synchronously.
	_, loaded := mgr.recoveryInFlight.Load("d1")
	assert.False(t, loaded, "in-flight flag should be cleared after handling")

	// State should have been reset since the event was processed.
	assert.Equal(t, types.DaemonStateUnknown, d.State())
}

func TestHandleDaemonDeathEvent_RemovedDaemonSkipped(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyNone)

	// Daemon not in cache — simulates concurrent removal.
	mgr.LivenessNotifier <- deathEvent{daemonID: "gone", path: "/test.sock"}
	close(mgr.LivenessNotifier)

	mgr.handleDaemonDeathEvent()

	// Should not panic, and no flag should be set.
	_, loaded := mgr.recoveryInFlight.Load("gone")
	assert.False(t, loaded)
}

func TestHandleDaemonDeathEvent_StaleProcessSkipped(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyNone)

	d := &daemon.Daemon{States: daemon.ConfigState{ID: "d1", ProcessID: 202}}
	mgr.daemonCache.Add(d)

	mgr.LivenessNotifier <- deathEvent{
		daemonID:  "d1",
		processID: 101,
		path:      "/test.sock",
	}
	close(mgr.LivenessNotifier)

	mgr.handleDaemonDeathEvent()

	assert.NotEqual(t, types.DaemonStateUnknown, d.State(),
		"a death event from the previous process generation must be ignored")
	_, loaded := mgr.recoveryInFlight.Load("d1")
	assert.False(t, loaded)
}

func TestIsRecoveryInFlightSerializesConcurrentCalls(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)

	const attempts = 20
	results := make(chan bool, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- mgr.isRecoveryInFlight("d1")
		}()
	}
	wg.Wait()
	close(results)

	// Exactly one caller observes "not yet in flight" and may start recovery.
	started := 0
	for inFlight := range results {
		if !inFlight {
			started++
		}
	}
	assert.Equal(t, 1, started, "only one concurrent recovery may start")

	mgr.recoveryInFlight.Delete("d1")
	assert.False(t, mgr.isRecoveryInFlight("d1"), "a new recovery may start after cleanup")
}

func TestClassifyProcessCmdline(t *testing.T) {
	const apisock = "/test.sock"

	// Process exited: /proc/<pid>/cmdline is gone.
	state, err := classifyProcessCmdline(nil, os.ErrNotExist, apisock)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessGone, state)

	// Zombie process: cmdline is empty.
	state, err = classifyProcessCmdline([]byte{}, nil, apisock)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessZombie, state)

	// Pid reused by another process serving a different socket.
	state, err = classifyProcessCmdline([]byte("nydusd\x00--apisock\x00/other.sock\x00"), nil, apisock)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessReused, state)

	// Alive and matching. Note: no `--id` on the command line for
	// restart-policy daemons, only `--apisock` is guaranteed.
	state, err = classifyProcessCmdline([]byte("nydusd\x00fuse\x00--apisock\x00/test.sock\x00"), nil, apisock)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessAlive, state)

	// An unexpected inspection failure must not be mistaken for process exit.
	_, err = classifyProcessCmdline(nil, os.ErrPermission, apisock)
	assert.Error(t, err)
}

func TestTerminateDaemonProcessNoPid(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)
	d := &daemon.Daemon{States: daemon.ConfigState{ID: "d1", APISocket: "/test.sock"}}
	assert.NoError(t, mgr.terminateDaemonProcess(d, "restart"))
}

func TestTerminateDaemonProcessKillsLiveProcess(t *testing.T) {
	apisock := "/tmp/nydus-term-sigterm.sock"
	d, cmd := startHelperDaemon(t, apisock, false /* ignoreSIGTERM */)

	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)
	assert.NoError(t, mgr.terminateDaemonProcess(d, "restart"))

	assert.False(t, processAlive(cmd.Process.Pid), "process should be gone after SIGTERM")
}

func TestTerminateDaemonProcessEscalatesToKill(t *testing.T) {
	origTimeout := defaultDaemonTerminationTimeout
	origPoll := processExitPollInterval
	t.Cleanup(func() {
		defaultDaemonTerminationTimeout = origTimeout
		processExitPollInterval = origPoll
	})
	defaultDaemonTerminationTimeout = 300 * time.Millisecond
	processExitPollInterval = 10 * time.Millisecond

	apisock := "/tmp/nydus-term-sigkill.sock"
	d, cmd := startHelperDaemon(t, apisock, true /* ignoreSIGTERM */)

	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)

	result := make(chan error, 1)
	go func() { result <- mgr.terminateDaemonProcess(d, "restart") }()

	select {
	case err := <-result:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("terminate did not finish; SIGKILL escalation may be broken")
	}

	assert.False(t, processAlive(cmd.Process.Pid), "process should be gone after SIGKILL")
}

func TestTerminateDaemonProcessSkipsReusedPid(t *testing.T) {
	// A live process whose cmdline does NOT match the daemon's --apisock must
	// be treated as a reused pid and left untouched.
	cmd := exec.Command("sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	d := &daemon.Daemon{States: daemon.ConfigState{
		ID:        "d1",
		APISocket: "/tmp/nydus-term-reused.sock",
		ProcessID: cmd.Process.Pid,
	}}

	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)
	assert.NoError(t, mgr.terminateDaemonProcess(d, "restart"))

	assert.True(t, processAlive(cmd.Process.Pid),
		"a process with an unrelated cmdline (reused pid) must not be killed")
}

// startHelperDaemon re-executes the test binary as a stand-in nydusd whose
// /proc/<pid>/cmdline carries "--apisock <apisock>", so inspectDaemonProcess
// classifies it as a live nydusd. terminateDaemonProcess reaps it via d.Wait().
func startHelperDaemon(t *testing.T, apisock string, ignoreSIGTERM bool) (*daemon.Daemon, *exec.Cmd) {
	t.Helper()

	readyFile := t.TempDir() + "/ready"
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperDaemonProcess$", "--", "--apisock", apisock)
	cmd.Env = append(
		os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_READY_FILE="+readyFile,
	)
	if ignoreSIGTERM {
		cmd.Env = append(cmd.Env, "GO_HELPER_IGNORE_SIGTERM=1")
	}
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		// terminateDaemonProcess may have already reaped it; ignore errors.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// The helper creates this file only after installing its signal handler.
	// Waiting for it ensures the SIGKILL escalation test cannot pass because
	// SIGTERM arrived before signal.Ignore took effect.
	require.Eventually(t, func() bool {
		_, err := os.Stat(readyFile)
		return err == nil
	}, 3*time.Second, 10*time.Millisecond, "helper process did not become ready")

	d := &daemon.Daemon{States: daemon.ConfigState{
		ID:        "d1",
		APISocket: apisock,
		ProcessID: cmd.Process.Pid,
	}}

	// Wait until /proc/<pid>/cmdline reflects the helper's arguments.
	require.Eventually(t, func() bool {
		state, err := inspectDaemonProcess(d)
		return err == nil && state == daemonProcessAlive
	}, 3*time.Second, 10*time.Millisecond, "helper process did not become inspectable")

	return d, cmd
}

// processAlive reports whether pid refers to a live (non-zombie) process.
func processAlive(pid int) bool {
	cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return false
	}
	// A reaped/zombie process has an empty cmdline.
	return len(cmdline) > 0
}

// TestHelperDaemonProcess is not a real test; it is the child process spawned
// by startHelperDaemon. It only runs when GO_WANT_HELPER_PROCESS is set.
func TestHelperDaemonProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("GO_HELPER_IGNORE_SIGTERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if err := os.WriteFile(os.Getenv("GO_HELPER_READY_FILE"), nil, 0o600); err != nil {
		os.Exit(1)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
