/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"os"
	"sync"
	"testing"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/stretchr/testify/assert"
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

func TestBeginDaemonRecoverySerializesConcurrentCalls(t *testing.T) {
	mgr := newTestManagerForEvents(config.RecoverPolicyRestart)

	const attempts = 20
	results := make(chan bool, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- !mgr.isRecoveryInFlight("d1")
		}()
	}
	wg.Wait()
	close(results)

	started := 0
	for result := range results {
		if result {
			started++
		}
	}
	assert.Equal(t, 1, started, "only one concurrent recovery may start")

	mgr.recoveryInFlight.Delete("d1")
	assert.False(t, mgr.isRecoveryInFlight("d1"), "a new recovery may start after cleanup")
}

func TestInspectDaemonProcess(t *testing.T) {
	origRead := readProcessCmdline
	t.Cleanup(func() { readProcessCmdline = origRead })

	d := &daemon.Daemon{States: daemon.ConfigState{ID: "d1", APISocket: "/test.sock", ProcessID: 123}}

	// Process exited: /proc/<pid>/cmdline is gone.
	readProcessCmdline = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	state, err := inspectDaemonProcess(d)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessGone, state)

	// Zombie process: cmdline is empty.
	readProcessCmdline = func(string) ([]byte, error) { return []byte{}, nil }
	state, err = inspectDaemonProcess(d)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessZombie, state)

	// Pid reused by another process serving a different socket.
	readProcessCmdline = func(string) ([]byte, error) {
		return []byte("nydusd\x00--apisock\x00/other.sock\x00"), nil
	}
	state, err = inspectDaemonProcess(d)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessReused, state)

	// Alive and matching. Note: no `--id` on the command line for
	// restart-policy daemons, only `--apisock` is guaranteed.
	readProcessCmdline = func(string) ([]byte, error) {
		return []byte("nydusd\x00fuse\x00--apisock\x00/test.sock\x00"), nil
	}
	state, err = inspectDaemonProcess(d)
	assert.NoError(t, err)
	assert.Equal(t, daemonProcessAlive, state)

	// An unexpected inspection failure must not be mistaken for process exit.
	readProcessCmdline = func(string) ([]byte, error) { return nil, os.ErrPermission }
	_, err = inspectDaemonProcess(d)
	assert.Error(t, err)
}
