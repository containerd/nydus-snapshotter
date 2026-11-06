//go:build linux

/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageInUseUntilLastContainerRemoved(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	ctx := context.Background()
	metaID, _ := createDockerChainNydusBase(t, sn)
	_, _, err := sn.createSnapshot(ctx, snapshots.KindActive, "c1", "meta", nil)
	require.NoError(t, err)
	_, _, err = sn.createSnapshot(ctx, snapshots.KindView, "v1", "meta", nil)
	require.NoError(t, err)

	for _, step := range []struct {
		remove string
		inUse  bool
	}{{"c1", true}, {"v1", false}} {
		require.NoError(t, sn.Remove(ctx, step.remove))
		inUse, err := sn.imageInUse(ctx, metaID)
		require.NoError(t, err)
		assert.Equal(t, step.inUse, inUse, "after removing %s", step.remove)
	}
}

// View must record its snapshot before mounting, or a concurrent Remove can
// release the nydusd it is about to use. A failing mount shows the order.
func TestViewRecordsSnapshotBeforeMounting(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	ctx := context.Background()
	createCommittedDockerChainLayer(t, sn, "data", "", map[string]string{label.NydusDataLayer: "true"})
	createCommittedDockerChainLayer(t, sn, "meta", "data", map[string]string{label.NydusMetaLayer: "true"})

	_, err := sn.View(ctx, "v1", "meta")
	require.Error(t, err, "no nydusd can be mounted in this test")
	_, err = sn.Stat(ctx, "v1")
	require.NoError(t, err)
}

func TestUnusedCacheBlobsKeepsBlobsOfCachedImages(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	cacheDir := t.TempDir()
	cm, err := cache.NewManager(cache.Opt{CacheDir: cacheDir})
	require.NoError(t, err)
	sn.fs, err = filesystem.NewFileSystem(context.Background(), filesystem.WithCacheManager(cm))
	require.NoError(t, err)

	createCommittedDockerChainLayer(t, sn, "data", "", map[string]string{
		label.NydusDataLayer: "true",
		label.CRILayerDigest: "sha256:cached",
	})
	for _, blob := range []string{"cached", "orphan"} {
		require.NoError(t, os.WriteFile(filepath.Join(cacheDir, blob+".blob.data"), nil, 0644))
	}

	unused, err := sn.getUnusedCacheBlobs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"orphan"}, unused)
}
