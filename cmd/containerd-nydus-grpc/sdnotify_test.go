/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifySystemd(t *testing.T) {
	t.Run("sends READY over NOTIFY_SOCKET", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "notify.sock")
		conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		t.Setenv("NOTIFY_SOCKET", sock)

		notifyReady()

		require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
		buf := make([]byte, 64)
		n, err := conn.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "READY=1", string(buf[:n]))

		notifyStopping()

		n, err = conn.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "STOPPING=1", string(buf[:n]))
	})

	t.Run("no-op without NOTIFY_SOCKET", func(t *testing.T) {
		t.Setenv("NOTIFY_SOCKET", "")

		notifyReady()
	})
}
