/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/layout"
	"github.com/stretchr/testify/require"
)

func TestWaitForReadyBootstrapWaitsForStableV6(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")
	require.NoError(t, writeFakeV6Bootstrap(bootstrap, 392192, v6BlockBits4096))

	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = writeFakeV6Bootstrap(bootstrap, 393216, v6BlockBits4096)
	}()

	err := waitForReadyBootstrapWithRetry(bootstrap, 20, 10*time.Millisecond)
	require.NoError(t, err)

	info, err := os.Stat(bootstrap)
	require.NoError(t, err)
	require.EqualValues(t, 393216, info.Size())
}

func TestWaitForReadyBootstrapRejectsStableMisalignedV6(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")
	require.NoError(t, writeFakeV6Bootstrap(bootstrap, 392192, v6BlockBits4096))

	err := waitForReadyBootstrapWithRetry(bootstrap, 3, 5*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not aligned")
}

func TestWaitForReadyBootstrapAcceptsStableV5(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")
	require.NoError(t, writeFakeV5Bootstrap(bootstrap, 8192))

	err := waitForReadyBootstrapWithRetry(bootstrap, 3, 5*time.Millisecond)
	require.NoError(t, err)
}

func writeFakeV5Bootstrap(path string, size int) error {
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], layout.RafsV5SuperMagic)
	binary.LittleEndian.PutUint32(buf[4:8], layout.RafsV5SuperVersion)
	return os.WriteFile(path, buf, 0644)
}

func writeFakeV6Bootstrap(path string, size int, blockBits byte) error {
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[layout.RafsV6SuperBlockOffset:layout.RafsV6SuperBlockOffset+4], layout.RafsV6SuperMagic)
	buf[v6BlockBitsOffset] = blockBits
	return os.WriteFile(path, buf, 0644)
}
