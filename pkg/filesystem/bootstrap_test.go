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

func TestWaitForReadyBootstrapWaitsForStableBlobMeta(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")
	blobMeta := filepath.Join(dir, "a.blob.meta")

	require.NoError(t, writeFakeV5Bootstrap(bootstrap, 8192))

	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = os.WriteFile(blobMeta, []byte{1, 2, 3}, 0644)
	}()

	err := waitForReadyBootstrapWithRetry(bootstrap, 5, 10*time.Millisecond)
	require.NoError(t, err)

	state, err := validateBootstrapAndBlobMeta(bootstrap)
	require.NoError(t, err)
	require.Equal(t, bootstrapState{
		BootstrapSize: 8192,
		BlobMetaFiles: []blobMetaState{
			{Name: "a.blob.meta", Size: 3},
		},
	}, state)
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

func TestValidateBootstrapRejectsTooSmall(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")

	require.NoError(t, os.WriteFile(bootstrap, make([]byte, 7), 0644))

	_, err := validateBootstrap(bootstrap)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bootstrap is too small")
}

func TestValidateBootstrapRejectsUnknownV6BlockBits(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")

	require.NoError(t, writeFakeV6Bootstrap(bootstrap, 393216, 10))

	_, err := validateBootstrap(bootstrap)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown v6 block bits")
}

func TestValidateBlobMetaFilesReturnsSortedState(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "z.blob.meta"), []byte{1, 2}, 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.blob.meta"), []byte{9}, 0644))

	state, err := validateBlobMetaFiles(bootstrap)
	require.NoError(t, err)
	require.Equal(t, []blobMetaState{
		{Name: "a.blob.meta", Size: 1},
		{Name: "z.blob.meta", Size: 2},
	}, state)
}

func TestValidateBlobMetaFilesRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.blob.meta"), nil, 0644))

	_, err := validateBlobMetaFiles(bootstrap)
	require.Error(t, err)
	require.Contains(t, err.Error(), "is empty")
}

func TestValidateBootstrapAndBlobMetaReturnsCombinedState(t *testing.T) {
	dir := t.TempDir()
	bootstrap := filepath.Join(dir, "image.boot")

	require.NoError(t, writeFakeV5Bootstrap(bootstrap, 8192))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "layer.blob.meta"), []byte{1, 2, 3, 4}, 0644))

	state, err := validateBootstrapAndBlobMeta(bootstrap)
	require.NoError(t, err)
	require.Equal(t, bootstrapState{
		BootstrapSize: 8192,
		BlobMetaFiles: []blobMetaState{
			{Name: "layer.blob.meta", Size: 4},
		},
	}, state)
}

func TestDetectV6BlockSize(t *testing.T) {
	head := make([]byte, v6BlockBitsOffset+1)

	head[v6BlockBitsOffset] = v6BlockBits512
	size, err := detectV6BlockSize(head)
	require.NoError(t, err)
	require.Equal(t, 512, size)

	head[v6BlockBitsOffset] = v6BlockBits4096
	size, err = detectV6BlockSize(head)
	require.NoError(t, err)
	require.Equal(t, 4096, size)
}

func TestDetectV6BlockSizeRejectsShortHeader(t *testing.T) {
	head := make([]byte, v6BlockBitsOffset)

	_, err := detectV6BlockSize(head)
	require.Error(t, err)
	require.Contains(t, err.Error(), "header is too small")
}

func TestBootstrapStateEqual(t *testing.T) {
	a := bootstrapState{
		BootstrapSize: 8192,
		BlobMetaFiles: []blobMetaState{
			{Name: "a.blob.meta", Size: 1},
			{Name: "b.blob.meta", Size: 2},
		},
	}

	b := bootstrapState{
		BootstrapSize: 8192,
		BlobMetaFiles: []blobMetaState{
			{Name: "a.blob.meta", Size: 1},
			{Name: "b.blob.meta", Size: 2},
		},
	}

	c := bootstrapState{
		BootstrapSize: 8193,
		BlobMetaFiles: []blobMetaState{
			{Name: "a.blob.meta", Size: 1},
			{Name: "b.blob.meta", Size: 2},
		},
	}

	d := bootstrapState{
		BootstrapSize: 8192,
		BlobMetaFiles: []blobMetaState{
			{Name: "a.blob.meta", Size: 1},
		},
	}

	require.True(t, a.Equal(b))
	require.False(t, a.Equal(c))
	require.False(t, a.Equal(d))
}

func writeFakeV5Bootstrap(path string, size int) error {
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], layout.RafsV5SuperMagic)
	binary.LittleEndian.PutUint32(buf[4:8], layout.RafsV5SuperVersion)
	return os.WriteFile(path, buf, 0644)
}

func writeFakeV6Bootstrap(path string, size int, blockBits byte) error {
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(
		buf[layout.RafsV6SuperBlockOffset:layout.RafsV6SuperBlockOffset+4],
		layout.RafsV6SuperMagic,
	)
	buf[v6BlockBitsOffset] = blockBits
	return os.WriteFile(path, buf, 0644)
}
