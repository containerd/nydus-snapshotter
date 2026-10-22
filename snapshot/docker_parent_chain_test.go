//go:build linux

/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	metadata "github.com/containerd/nydus-snapshotter/pkg/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These descriptor-level tests exercise real metadata and snapshotter entry
// points, but never invoke mount(2), nydusd, Docker or containerd services.
// Do not make them parallel: production configuration and RAFS cache are global.
func newDockerChainTestSnapshotter(t *testing.T) *snapshotter {
	t.Helper()
	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() { rafs.RafsGlobalCache.SetIntances(originalInstances) })
	root := t.TempDir()
	cfg := &config.SnapshotterConfig{Root: root, DaemonMode: string(config.DaemonModeDedicated)}
	cfg.DaemonConfig.FsDriver = config.FsDriverFusedev
	require.NoError(t, config.ProcessConfigurations(cfg))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "snapshots"), 0755))
	ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
	require.NoError(t, err)
	sn := &snapshotter{root: root, ms: ms, fs: &filesystem.Filesystem{}}
	t.Cleanup(func() {
		if sn.ms != nil {
			require.NoError(t, sn.ms.Close())
		}
	})
	return sn
}

func createCommittedDockerChainLayer(t *testing.T, sn *snapshotter, key, parent string, labels map[string]string) storage.Snapshot {
	t.Helper()
	_, s, err := sn.createSnapshot(context.Background(), snapshots.KindActive, key+"-active", parent, []snapshots.Opt{snapshots.WithLabels(labels)})
	require.NoError(t, err)
	ctx, tx, err := sn.ms.TransactionContext(context.Background(), true)
	require.NoError(t, err)
	_, err = storage.CommitActive(ctx, key+"-active", key, snapshots.Usage{}, snapshots.WithLabels(labels))
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	require.NoError(t, tx.Commit())
	return s
}

// data/fs is a placeholder and meta/fs contains bootstrap artifacts. Only
// meta/mnt represents the image rootfs; asserting this path catches an incorrect
// native-mount fallback even though no kernel mount is performed.
func createDockerChainNydusBase(t *testing.T, sn *snapshotter) (string, string) {
	t.Helper()
	createCommittedDockerChainLayer(t, sn, "data", "", map[string]string{label.NydusDataLayer: "true"})
	meta := createCommittedDockerChainLayer(t, sn, "meta", "data", map[string]string{label.NydusMetaLayer: "true"})
	rootfs := filepath.Join(sn.root, "snapshots", meta.ID, "mnt")
	require.NoError(t, os.MkdirAll(rootfs, 0755))
	// A cache-only test driver makes WaitUntilReady independent of a real daemon.
	rafs.RafsGlobalCache.Add(&rafs.Rafs{SnapshotID: meta.ID, FsDriver: "test-cache-only", Mountpoint: rootfs})
	return meta.ID, rootfs
}

func dockerChainLowerdir(t *testing.T, mounts []mount.Mount) []string {
	t.Helper()
	require.Len(t, mounts, 1)
	require.Equal(t, "overlay", mounts[0].Type)
	for _, opt := range mounts[0].Options {
		if strings.HasPrefix(opt, "lowerdir=") {
			return strings.Split(strings.TrimPrefix(opt, "lowerdir="), ":")
		}
	}
	t.Fatalf("no lowerdir in %#v", mounts)
	return nil
}

func TestDockerNydusActiveParentChain(t *testing.T) {
	for _, ordinaryLayers := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("ordinary_layers_%d", ordinaryLayers), func(t *testing.T) {
			sn := newDockerChainTestSnapshotter(t)
			_, rootfs := createDockerChainNydusBase(t, sn)
			parent := "meta"
			wantLower := []string{rootfs}
			for i := 0; i < ordinaryLayers; i++ {
				key := fmt.Sprintf("ordinary-%d", i)
				s := createCommittedDockerChainLayer(t, sn, key, parent, nil)
				wantLower = append([]string{sn.upperPath(s.ID)}, wantLower...)
				parent = key
			}
			prepared, err := sn.Prepare(context.Background(), "container", parent)
			require.NoError(t, err)
			assert.Equal(t, wantLower, dockerChainLowerdir(t, prepared), "Prepare must preserve every layer above the mounted Nydus rootfs")
			retrieved, err := sn.Mounts(context.Background(), "container")
			require.NoError(t, err)
			assert.Equal(t, wantLower, dockerChainLowerdir(t, retrieved), "Mounts must agree with Prepare")
			assert.Equal(t, prepared, retrieved)
		})
	}
}

// Construct the pre-fix on-disk shape without Prepare, then reopen its database.
// This checks descriptor reconstruction, not real RAFS daemon recovery/mounting.
func TestDockerNydusPersistedActiveParentChain(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	ctx := context.Background()
	commit := func(key, parent string, labels map[string]string) storage.Snapshot {
		t.Helper()
		_, s, err := sn.createSnapshot(ctx, snapshots.KindActive, key+"-active", parent, []snapshots.Opt{snapshots.WithLabels(labels)})
		require.NoError(t, err)
		require.NoError(t, sn.Commit(ctx, key, key+"-active", snapshots.WithLabels(labels)))
		return s
	}
	data := commit("data", "", map[string]string{label.NydusDataLayer: "true"})
	meta := commit("meta", "data", map[string]string{label.NydusMetaLayer: "true"})
	init := commit("init", "meta", map[string]string{"fixture": "existing-init"})
	_, active, err := sn.createSnapshot(ctx, snapshots.KindActive, "container", "init", []snapshots.Opt{
		snapshots.WithLabels(map[string]string{"fixture": "existing-container"}),
	})
	require.NoError(t, err)
	require.Equal(t, []string{init.ID, meta.ID, data.ID}, active.ParentIDs)
	files := map[string]string{
		filepath.Join(sn.upperPath(init.ID), ".dockerenv"): "existing init data",
		filepath.Join(sn.upperPath(active.ID), "kept.txt"): "existing writable data",
	}
	for name, contents := range files {
		require.NoError(t, os.WriteFile(name, []byte(contents), 0644))
	}
	type record struct {
		id    string
		info  snapshots.Info
		usage snapshots.Usage
	}
	before := make(map[string]record)
	for _, key := range []string{"data", "meta", "init", "container"} {
		id, info, usage, err := metadata.GetSnapshotInfo(ctx, sn.ms, key)
		require.NoError(t, err)
		before[key] = record{id, info, usage}
	}
	beforeActive, err := metadata.GetSnapshot(ctx, sn.ms, "container")
	require.NoError(t, err)
	oldMS := sn.ms
	sn.ms = nil
	require.NoError(t, oldMS.Close())
	sn.ms, err = storage.NewMetaStore(filepath.Join(sn.root, "metadata.db"))
	require.NoError(t, err)
	sn.fs = &filesystem.Filesystem{}
	rootfs := filepath.Join(sn.root, "snapshots", meta.ID, "mnt")
	require.NoError(t, os.MkdirAll(rootfs, 0755))
	// Model the RAFS instance recovered independently from the snapshot DB.
	rafs.RafsGlobalCache.Add(&rafs.Rafs{SnapshotID: meta.ID, FsDriver: "test-cache-only", Mountpoint: rootfs})
	mounts, err := sn.Mounts(ctx, "container")
	require.NoError(t, err)
	require.Equal(t, []string{sn.upperPath(init.ID), rootfs}, dockerChainLowerdir(t, mounts))
	require.Contains(t, mounts[0].Options, "upperdir="+sn.upperPath(active.ID))
	require.Contains(t, mounts[0].Options, "workdir="+sn.workPath(active.ID))
	for key, want := range before {
		id, info, usage, err := metadata.GetSnapshotInfo(ctx, sn.ms, key)
		require.NoError(t, err)
		assert.Equal(t, want, record{id, info, usage}, "persisted record changed for %s", key)
	}
	afterActive, err := metadata.GetSnapshot(ctx, sn.ms, "container")
	require.NoError(t, err)
	assert.Equal(t, beforeActive, afterActive)
	for name, contents := range files {
		got, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, contents, string(got))
	}
}

func TestDockerNydusMappedActiveParentChain(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	metaID, sourceRootfs := createDockerChainNydusBase(t, sn)
	init := createCommittedDockerChainLayer(t, sn, "init", "meta", nil)
	_, err := sn.Prepare(context.Background(), "container", "init")
	require.NoError(t, err)

	// Add the mapping after creation so metadata-only tests do not require
	// privileges to chown the upper directory to another host UID.
	info, err := sn.Stat(context.Background(), "container")
	require.NoError(t, err)
	info.Labels = map[string]string{
		label.SnapshotUIDMapping: "0:100000:65536",
		label.SnapshotGIDMapping: "0:100000:65536",
	}
	_, err = sn.Update(context.Background(), info, "labels")
	require.NoError(t, err)
	mappedID, err := label.RafsInstanceIDFromLabels(metaID, info.Labels)
	require.NoError(t, err)
	mappedRootfs := filepath.Join(sn.root, "mnt", mappedID)
	require.NoError(t, os.MkdirAll(mappedRootfs, 0755))
	// Only the mapped instance exists: using the metadata layer's labels when
	// waiting would select the absent, unmapped instance and fail this test.
	rafs.RafsGlobalCache.Remove(metaID)
	rafs.RafsGlobalCache.Add(&rafs.Rafs{
		SnapshotID: mappedID, SourceSnapshotID: metaID,
		FsDriver: "test-cache-only", Mountpoint: mappedRootfs,
	})
	retrieved, err := sn.Mounts(context.Background(), "container")
	require.NoError(t, err)
	lower := dockerChainLowerdir(t, retrieved)
	assert.Equal(t, []string{sn.upperPath(init.ID), mappedRootfs}, lower)
	assert.NotContains(t, lower, sourceRootfs)
}

func TestDockerNydusActiveMetaUnpackRemainsNative(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	data := createCommittedDockerChainLayer(t, sn, "data", "", map[string]string{label.NydusDataLayer: "true"})
	// An Active bootstrap-unpack destination can itself carry a meta label.
	// Mounts must search its parent rather than treating this destination as
	// an already-mounted Nydus filesystem instance.
	labels := map[string]string{
		label.NydusMetaLayer: "true", label.TargetSnapshotRef: "meta",
	}
	prepared, err := sn.Prepare(context.Background(), "unpack", "data", snapshots.WithLabels(labels))
	require.NoError(t, err)
	active, err := metadata.GetSnapshot(context.Background(), sn.ms, "unpack")
	require.NoError(t, err)
	rootfs := filepath.Join(sn.root, "snapshots", active.ID, "mnt")
	require.NoError(t, os.MkdirAll(rootfs, 0755))
	rafs.RafsGlobalCache.Add(&rafs.Rafs{
		SnapshotID: active.ID,
		FsDriver:   "test-cache-only",
		Mountpoint: rootfs,
	})
	require.Equal(t, []string{sn.upperPath(data.ID)}, dockerChainLowerdir(t, prepared))
	retrieved, err := sn.Mounts(context.Background(), "unpack")
	require.NoError(t, err)
	assert.Equal(t, prepared, retrieved)
	assert.NotContains(t, dockerChainLowerdir(t, retrieved), rootfs)
}

func TestDockerNydusMissingAncestorInstanceDoesNotFallback(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	metaID, _ := createDockerChainNydusBase(t, sn)
	createCommittedDockerChainLayer(t, sn, "init", "meta", nil)
	_, _, err := sn.createSnapshot(context.Background(), snapshots.KindActive, "container", "init", nil)
	require.NoError(t, err)
	rafs.RafsGlobalCache.Remove(metaID)
	mounts, err := sn.Mounts(context.Background(), "container")
	require.ErrorIs(t, err, errdefs.ErrNotFound)
	require.ErrorContains(t, err, "is not ready")
	require.Nil(t, mounts, "a known Nydus ancestor must not fall back to bootstrap/blob directories")
}

func TestDockerNydusOrdinaryOCIStaysNative(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	base := createCommittedDockerChainLayer(t, sn, "ordinary-base", "", nil)
	init := createCommittedDockerChainLayer(t, sn, "ordinary-init", "ordinary-base", nil)
	want := []string{sn.upperPath(init.ID), sn.upperPath(base.ID)}
	prepared, err := sn.Prepare(context.Background(), "container", "ordinary-init")
	require.NoError(t, err)
	require.Equal(t, want, dockerChainLowerdir(t, prepared))
	retrieved, err := sn.Mounts(context.Background(), "container")
	require.NoError(t, err)
	require.Equal(t, prepared, retrieved)
}

func TestDockerNydusUnrelatedMetaIsNotAncestor(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	_, unrelatedRootfs := createDockerChainNydusBase(t, sn)
	ordinary := createCommittedDockerChainLayer(t, sn, "ordinary", "", nil)
	prepared, err := sn.Prepare(context.Background(), "container", "ordinary")
	require.NoError(t, err)
	require.Equal(t, []string{sn.upperPath(ordinary.ID)}, dockerChainLowerdir(t, prepared))
	require.NotContains(t, dockerChainLowerdir(t, prepared), unrelatedRootfs)
	retrieved, err := sn.Mounts(context.Background(), "container")
	require.NoError(t, err)
	require.Equal(t, prepared, retrieved)
}

// Retain the direct-meta View contract, including the empty view/fs lowerdir
// used to supply a second lowerdir on older Linux OverlayFS implementations.
// Mounts(viewKey) and a View with intermediate OCI ancestors are separate,
// pre-existing behavior gaps; this minimal Active-snapshot fix does not claim
// to address either one.
func TestDockerNydusViewDirectMetaCompatibility(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	_, rootfs := createDockerChainNydusBase(t, sn)
	prepared, err := sn.View(context.Background(), "view", "meta")
	require.NoError(t, err)
	view, err := metadata.GetSnapshot(context.Background(), sn.ms, "view")
	require.NoError(t, err)
	assertDockerNydusReadonlyMount(t, prepared, []string{rootfs, sn.upperPath(view.ID)})
}

func TestDockerNydusOCIViewRemainsReadonlyBind(t *testing.T) {
	sn := newDockerChainTestSnapshotter(t)
	base := createCommittedDockerChainLayer(t, sn, "ordinary", "", nil)
	prepared, err := sn.View(context.Background(), "view", "ordinary")
	require.NoError(t, err)
	assertDockerNydusReadonlyMount(t, prepared, []string{sn.upperPath(base.ID)})
	retrieved, err := sn.Mounts(context.Background(), "view")
	require.NoError(t, err)
	require.Equal(t, prepared, retrieved)
}

func assertDockerNydusReadonlyMount(t *testing.T, mounts []mount.Mount, wantLower []string) {
	t.Helper()
	require.Len(t, mounts, 1)
	for _, opt := range mounts[0].Options {
		require.False(t, strings.HasPrefix(opt, "upperdir=") || strings.HasPrefix(opt, "workdir="), "readonly view has writable overlay fields: %v", mounts)
	}
	if mounts[0].Type == "bind" {
		require.Len(t, wantLower, 1)
		require.Equal(t, wantLower[0], mounts[0].Source)
		require.Contains(t, mounts[0].Options, "ro")
		return
	}
	require.Equal(t, wantLower, dockerChainLowerdir(t, mounts))
}
