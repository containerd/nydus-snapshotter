/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package manager

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
)

func TestTerminateFailedDaemon(t *testing.T) {
	orig := defaultDaemonTerminationTimeout
	t.Cleanup(func() { defaultDaemonTerminationTimeout = orig })

	m := &Manager{}

	t.Run("nil process is a no-op", func(t *testing.T) {
		d, err := daemon.NewDaemon()
		require.NoError(t, err)
		m.terminateFailedDaemon(d, nil)
	})

	t.Run("SIGTERM stops a well-behaved process", func(t *testing.T) {
		defaultDaemonTerminationTimeout = 5 * time.Second

		cmd := exec.Command("sleep", "300")
		require.NoError(t, cmd.Start())
		d, err := daemon.NewDaemon()
		require.NoError(t, err)

		start := time.Now()
		m.terminateFailedDaemon(d, cmd.Process)

		// The process is reaped inside the helper, so it is marked done.
		assert.ErrorIs(t, cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone)
		// A SIGTERM-able process must exit well before the SIGKILL escalation.
		assert.Less(t, time.Since(start), defaultDaemonTerminationTimeout)
	})

	t.Run("escalates to SIGKILL when SIGTERM is ignored", func(t *testing.T) {
		defaultDaemonTerminationTimeout = 200 * time.Millisecond

		// Ignore SIGTERM so only SIGKILL can stop it.
		cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 300")
		require.NoError(t, cmd.Start())
		d, err := daemon.NewDaemon()
		require.NoError(t, err)

		m.terminateFailedDaemon(d, cmd.Process)

		assert.ErrorIs(t, cmd.Process.Signal(syscall.Signal(0)), os.ErrProcessDone)
	})
}
