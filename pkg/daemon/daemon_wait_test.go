/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForExitReapsChildProcess(t *testing.T) {
	cmd := exec.Command("sleep", "1000")
	require.NoError(t, cmd.Start())

	d := &Daemon{
		States: ConfigState{
			ProcessID: cmd.Process.Pid,
		},
	}

	done := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		done <- cmd.Process.Kill()
	}()

	require.NoError(t, d.WaitForExit())
	require.NoError(t, <-done)

	_, err := cmd.Process.Wait()
	require.Error(t, err)
}
