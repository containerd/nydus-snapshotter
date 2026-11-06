/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMountNative(t *testing.T) {
	// Create a minimal snapshotter instance for testing
	snapshotterRoot := "/var/lib/containerd/snapshotter"
	s := &snapshotter{
		root: snapshotterRoot,
	}

	tests := []struct {
		name           string
		snapshot       storage.Snapshot
		labels         map[string]string
		expectedMounts []mount.Mount

		validate func(t *testing.T, mounts []mount.Mount, s *snapshotter)
	}{
		{
			name: "no parents, active snapshot - rw bind mount",
			snapshot: storage.Snapshot{
				ID:        "snap1",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("snap1"),
					Options: []string{
						"rw",
						"rbind",
					},
				},
			},
		},
		{
			name: "no parents, view snapshot - ro bind mount",
			snapshot: storage.Snapshot{
				ID:        "snap2",
				Kind:      snapshots.KindView,
				ParentIDs: []string{},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("snap2"),
					Options: []string{
						"ro",
						"rbind",
					},
				},
			},
		},
		{
			name: "one parent, committed snapshot - ro bind mount of parent",
			snapshot: storage.Snapshot{
				ID:        "snap3",
				Kind:      snapshots.KindCommitted,
				ParentIDs: []string{"parent1"},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("parent1"),
					Options: []string{
						"ro",
						"rbind",
					},
				},
			},
		},
		{
			name: "one parent, view snapshot - ro bind mount of parent",
			snapshot: storage.Snapshot{
				ID:        "snap4",
				Kind:      snapshots.KindView,
				ParentIDs: []string{"parent1"},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("parent1"),
					Options: []string{
						"ro",
						"rbind",
					},
				},
			},
		},
		{
			name: "multiple parents, active snapshot - overlay with work and upper dirs",
			snapshot: storage.Snapshot{
				ID:        "snap5",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{"parent1", "parent2"},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("workdir=%s", s.workPath("snap5")),
						fmt.Sprintf("upperdir=%s", s.upperPath("snap5")),
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parent1"), s.upperPath("parent2")),
					},
				},
			},
		},
		{
			name: "multiple parents, active snapshot with volatile option",
			snapshot: storage.Snapshot{
				ID:        "snap6",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{"parent1", "parent2"},
			},
			labels: map[string]string{
				label.OverlayfsVolatileOpt: "true",
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("workdir=%s", s.workPath("snap6")),
						fmt.Sprintf("upperdir=%s", s.upperPath("snap6")),
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parent1"), s.upperPath("parent2")),
						"volatile",
					},
				},
			},
		},
		{
			name: "multiple parents, committed snapshot - overlay with only lowerdir",
			snapshot: storage.Snapshot{
				ID:        "snap7",
				Kind:      snapshots.KindCommitted,
				ParentIDs: []string{"parent1", "parent2", "parent3"},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("lowerdir=%s:%s:%s", s.upperPath("parent1"), s.upperPath("parent2"), s.upperPath("parent3")),
					},
				},
			},
		},
		{
			name: "multiple parents, view snapshot - overlay with only lowerdir",
			snapshot: storage.Snapshot{
				ID:        "snap8",
				Kind:      snapshots.KindView,
				ParentIDs: []string{"parent1", "parent2"},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parent1"), s.upperPath("parent2")),
					},
				},
			},
		},
		{
			name: "no parents, committed snapshot - rw bind mount",
			snapshot: storage.Snapshot{
				ID:        "snap9",
				Kind:      snapshots.KindCommitted,
				ParentIDs: []string{},
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: filepath.Join(s.root, "snapshots", "snap9", "fs"),
					Options: []string{
						"rw",
						"rbind",
					},
				},
			},
		},
		{
			name: "no parents, active snapshot with idmap - rw bind mount with uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "snap10",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{},
			},
			labels: map[string]string{
				snapshots.LabelSnapshotUIDMapping: "0:100000:65536",
				snapshots.LabelSnapshotGIDMapping: "0:100000:65536",
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("snap10"),
					Options: []string{
						"rw",
						"rbind",
						"uidmap=0:100000:65536",
						"gidmap=0:100000:65536",
					},
				},
			},
		},
		{
			name: "one parent, view snapshot with idmap - ro bind mount with uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "snap11",
				Kind:      snapshots.KindView,
				ParentIDs: []string{"parent1"},
			},
			labels: map[string]string{
				snapshots.LabelSnapshotUIDMapping: "0:200000:65536",
				snapshots.LabelSnapshotGIDMapping: "0:200000:65536",
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "bind",
					Source: s.upperPath("parent1"),
					Options: []string{
						"ro",
						"rbind",
						"uidmap=0:200000:65536",
						"gidmap=0:200000:65536",
					},
				},
			},
		},
		{
			name: "multiple parents, active snapshot with idmap - overlay with uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "snap12",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{"parent1", "parent2"},
			},
			labels: map[string]string{
				snapshots.LabelSnapshotUIDMapping: "0:100000:65536",
				snapshots.LabelSnapshotGIDMapping: "0:100000:65536",
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						fmt.Sprintf("workdir=%s", s.workPath("snap12")),
						fmt.Sprintf("upperdir=%s", s.upperPath("snap12")),
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parent1"), s.upperPath("parent2")),
						"uidmap=0:100000:65536",
						"gidmap=0:100000:65536",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Call the mountNative function
			mounts, err := s.mountNative(ctx, tt.labels, tt.snapshot)

			// Verify no error occurred
			require.NoError(t, err)

			// Verify expected number of mounts
			require.Len(t, mounts, len(tt.expectedMounts))
			for i, expectedMount := range tt.expectedMounts {
				// Verify expected mount
				assert.Equal(t, expectedMount.Type, mounts[i].Type)
				assert.Equal(t, expectedMount.Source, mounts[i].Source)
				assert.ElementsMatch(t, expectedMount.Options, mounts[i].Options)
			}
		})
	}
}

func TestMountNativeConfigVolatile(t *testing.T) {
	snapshotterRoot := "/var/lib/containerd/snapshotter"
	s := &snapshotter{
		root:                    snapshotterRoot,
		enableOverlayfsVolatile: true,
	}

	ctx := context.Background()

	t.Run("active snapshot gets volatile from config", func(t *testing.T) {
		snap := storage.Snapshot{
			ID:        "snap1",
			Kind:      snapshots.KindActive,
			ParentIDs: []string{"parent1", "parent2"},
		}
		mounts, err := s.mountNative(ctx, nil, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.Contains(t, mounts[0].Options, "volatile")
	})

	t.Run("view snapshot does not get volatile from config", func(t *testing.T) {
		snap := storage.Snapshot{
			ID:        "snap2",
			Kind:      snapshots.KindView,
			ParentIDs: []string{"parent1", "parent2"},
		}
		mounts, err := s.mountNative(ctx, nil, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.NotContains(t, mounts[0].Options, "volatile")
	})

	t.Run("committed snapshot does not get volatile from config", func(t *testing.T) {
		snap := storage.Snapshot{
			ID:        "snap3",
			Kind:      snapshots.KindCommitted,
			ParentIDs: []string{"parent1", "parent2"},
		}
		mounts, err := s.mountNative(ctx, nil, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.NotContains(t, mounts[0].Options, "volatile")
	})
}

func TestCollectUsedCacheBlobIDs(t *testing.T) {
	blobA := "1f66d51e5b3a51e0a4e942f89e2b9c4a7c76b0678c1c1e74dcb0f5e8a4b1c001"
	blobB := "2a77e62f6c4b62f1b5fa53f9af3cad5b8d87c1789d2d2f85edc1a6f9b5c2d002"

	known := &rafs.Rafs{
		SnapshotID:      "1",
		FsDriver:        "fusedev",
		UnderlyingFiles: []string{"/cache/" + blobA, "/cache/" + blobB + ".blob.meta"},
	}
	otherDriver := &rafs.Rafs{SnapshotID: "2", FsDriver: "blockdev"}

	// All blobcache-backed instances report their files: the set is complete
	// and safe to delete against.
	used, complete := collectUsedCacheBlobIDs([]*rafs.Rafs{known, otherDriver})
	assert.True(t, complete)
	assert.Equal(t, map[string]bool{blobA: true, blobB: true}, used)

	// An instance whose usage is not known yet (mounted, but Mounts() has not
	// populated UnderlyingFiles) makes the set incomplete: deleting based on
	// it would unlink cache files under a live daemon.
	unknown := &rafs.Rafs{SnapshotID: "3", FsDriver: "fusedev"}
	_, complete = collectUsedCacheBlobIDs([]*rafs.Rafs{known, unknown})
	assert.False(t, complete)
}

func TestGetCleanupDirectoriesProtectsOnlyIDMappedInstances(t *testing.T) {
	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() {
		rafs.RafsGlobalCache.SetIntances(originalInstances)
	})

	snapshotterRoot := t.TempDir()
	ms, err := storage.NewMetaStore(filepath.Join(snapshotterRoot, "metadata.db"))
	require.NoError(t, err)
	s := &snapshotter{
		root: snapshotterRoot,
		ms:   ms,
	}

	// A committed snapshot that containerd still tracks: it appears in the
	// IDMap and represents a genuinely-live ordinary instance.
	txCtx, trans, err := ms.TransactionContext(context.Background(), true)
	require.NoError(t, err)
	_, err = storage.CreateSnapshot(txCtx, snapshots.KindActive, "live-key", "")
	require.NoError(t, err)
	liveID, err := storage.CommitActive(txCtx, "live-key", "live", snapshots.Usage{})
	require.NoError(t, err)
	require.NoError(t, trans.Commit())

	// An orphaned ordinary instance and an orphaned idmapped instance: both
	// exist on disk and are still referenced in the RAFS cache, but neither is
	// tracked by containerd anymore (as after image GC removed their metadata).
	// The idmapped per-pod instance carries a SourceSnapshotID (set only when
	// remap-ids labels are present); ordinary instances leave it empty.
	const ordinaryID = "48"
	const idMappedSource = "50"
	idMappedID := label.RafsInstanceID(idMappedSource, &label.IDMapping{External: 100000, Range: 65536})
	instances := []*rafs.Rafs{
		{SnapshotID: liveID, SnapshotDir: s.snapshotDir(liveID)},
		{SnapshotID: ordinaryID, SnapshotDir: s.snapshotDir(ordinaryID)},
		{SnapshotID: idMappedID, SnapshotDir: s.snapshotDir(idMappedID), SourceSnapshotID: idMappedSource},
	}
	for _, instance := range instances {
		require.NoError(t, os.MkdirAll(instance.SnapshotDir, 0o755))
		rafs.RafsGlobalCache.Add(instance)
	}

	cleanup, err := s.cleanupDirectories(context.Background())
	require.NoError(t, err)

	// The ordinary orphan must be eligible for cleanup, otherwise its mount is
	// never torn down and its cached blobs are never released.
	assert.Contains(t, cleanup, s.snapshotDir(ordinaryID))
	// A live ordinary instance stays protected via the IDMap, and the idmapped
	// per-pod instance (which containerd's IDMap never tracks) stays protected
	// while still referenced.
	assert.NotContains(t, cleanup, s.snapshotDir(liveID))
	assert.NotContains(t, cleanup, s.snapshotDir(idMappedID))
}

func TestMountRemoteUsesPerPodLowerdirForIDMap(t *testing.T) {
	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() {
		rafs.RafsGlobalCache.SetIntances(originalInstances)
	})

	snapshotterRoot := t.TempDir()
	s := &snapshotter{
		root: snapshotterRoot,
		fs:   &filesystem.Filesystem{},
	}

	labels := map[string]string{
		label.SnapshotUIDMapping: "0:100000:65536",
		label.SnapshotGIDMapping: "0:100000:65536",
	}
	rafsKey, err := label.RafsInstanceIDFromLabels("meta", labels)
	require.NoError(t, err)

	mountpoint := filepath.Join(snapshotterRoot, "mnt", rafsKey)
	rafs.RafsGlobalCache.Add(&rafs.Rafs{
		SnapshotID:  rafsKey,
		SnapshotDir: filepath.Join(snapshotterRoot, "snapshots", "meta"),
		Mountpoint:  mountpoint,
	})

	mounts, err := s.mountRemote(context.Background(), labels, storage.Snapshot{
		ID:   "active",
		Kind: snapshots.KindActive,
	}, "meta", "active-key")
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Contains(t, mounts[0].Options, fmt.Sprintf("lowerdir=%s", mountpoint))
	assert.NotContains(t, mounts[0].Options, fmt.Sprintf("lowerdir=%s", filepath.Join(snapshotterRoot, "snapshots", "meta", "fs")))
}

func TestRemoveCleansUpDirectoriesOnlyAfterCommit(t *testing.T) {
	// The commit-failure injection changes the process-wide file size limit.
	// Run it without the parent's -test.testlogfile so a buffered test-cache
	// log write cannot fail while regular file writes are forbidden.
	const childEnv = "NYDUS_TEST_REMOVE_FAILURE_CHILD"
	if os.Getenv(childEnv) != t.Name() {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"="+t.Name())
		if coverDir := os.Getenv("GOCOVERDIR"); testing.CoverMode() != "" && coverDir != "" {
			cmd.Args = append(cmd.Args, "-test.gocoverdir="+coverDir)
		}
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "isolated test failed:\n%s", output)
		return
	}

	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() {
		rafs.RafsGlobalCache.SetIntances(originalInstances)
	})

	snapshotterRoot := t.TempDir()
	ms, err := storage.NewMetaStore(filepath.Join(snapshotterRoot, "metadata.db"))
	require.NoError(t, err)
	s := &snapshotter{
		root:       snapshotterRoot,
		ms:         ms,
		fs:         &filesystem.Filesystem{},
		syncRemove: true,
	}

	txCtx, trans, err := ms.TransactionContext(context.Background(), true)
	require.NoError(t, err)
	_, err = storage.CreateSnapshot(txCtx, snapshots.KindActive, "tmp-key", "")
	require.NoError(t, err)
	committedID, err := storage.CommitActive(txCtx, "tmp-key", "committed", snapshots.Usage{})
	require.NoError(t, err)
	require.NoError(t, trans.Commit())

	committedDir := s.snapshotDir(committedID)
	orphanDir := s.snapshotDir("99")
	require.NoError(t, os.MkdirAll(committedDir, 0o755))
	require.NoError(t, os.MkdirAll(orphanDir, 0o755))

	// A failed removal must not delete anything: the transaction did not
	// commit, so on-disk state has to stay consistent with the metadata.
	require.Error(t, s.Remove(context.Background(), "no-such-key"))
	assert.DirExists(t, committedDir)
	assert.DirExists(t, orphanDir)

	// The same holds when the metadata removal succeeds but its commit fails.
	restore := forbidFileWrites(t)
	err = s.Remove(context.Background(), "committed")
	restore()
	require.Error(t, err)
	assert.DirExists(t, committedDir)
	assert.DirExists(t, orphanDir)

	// A successful removal commits the metadata change first, then removes the
	// now-orphaned directories.
	require.NoError(t, s.Remove(context.Background(), "committed"))
	assert.NoDirExists(t, committedDir)
	assert.NoDirExists(t, orphanDir)

	cleanup, err := s.cleanupDirectories(context.Background())
	require.NoError(t, err)
	assert.Empty(t, cleanup)
}

// forbidFileWrites sets the soft RLIMIT_FSIZE to zero, so every write to a
// regular file fails with EFBIG until the returned function restores the limit.
// Go ignores SIGXFSZ, so the write returns an error instead of killing the
// process. bolt's commit hits this on its first page write.
func forbidFileWrites(t *testing.T) func() {
	var old syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &old))
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: old.Max}))
	return func() {
		require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &old))
	}
}

func TestRemoveReleasesImageOnlyWhenUnused(t *testing.T) {
	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() {
		rafs.RafsGlobalCache.SetIntances(originalInstances)
	})

	snapshotterRoot := t.TempDir()
	cfg := &config.SnapshotterConfig{Root: snapshotterRoot, DaemonMode: string(config.DaemonModeDedicated)}
	cfg.DaemonConfig.FsDriver = config.FsDriverFusedev
	require.NoError(t, config.ProcessConfigurations(cfg))
	require.NoError(t, os.Mkdir(filepath.Join(snapshotterRoot, "snapshots"), 0o755))
	ms, err := storage.NewMetaStore(filepath.Join(snapshotterRoot, "metadata.db"))
	require.NoError(t, err)
	s := &snapshotter{
		root: snapshotterRoot,
		ms:   ms,
		fs:   &filesystem.Filesystem{},
	}
	ctx := context.Background()

	_, _, err = s.createSnapshot(ctx, snapshots.KindActive, "meta-key", "", nil)
	require.NoError(t, err)
	txCtx, trans, err := ms.TransactionContext(ctx, true)
	require.NoError(t, err)
	metaID, err := storage.CommitActive(txCtx, "meta-key", "meta", snapshots.Usage{},
		snapshots.WithLabels(map[string]string{label.NydusMetaLayer: "true"}))
	require.NoError(t, err)
	require.NoError(t, trans.Commit())

	// View records the view before mounting, so a Remove racing that mount
	// sees the image in use. No nydusd can start here, so the mount fails.
	_, err = s.View(ctx, "v1", "meta")
	require.Error(t, err)
	_, _, err = s.createSnapshot(ctx, snapshots.KindActive, "c1", "meta", nil)
	require.NoError(t, err)

	// The image's nydusd may only be released once neither the container nor
	// the view uses it.
	for _, step := range []struct {
		remove string
		inUse  bool
	}{{"c1", true}, {"v1", false}} {
		require.NoError(t, s.Remove(ctx, step.remove))
		inUse, err := s.imageInUse(ctx, metaID)
		require.NoError(t, err)
		assert.Equal(t, step.inUse, inUse, "after removing %s", step.remove)
	}
}

func TestUnusedCacheBlobsKeepsBlobsOfCachedImages(t *testing.T) {
	snapshotterRoot := t.TempDir()
	ms, err := storage.NewMetaStore(filepath.Join(snapshotterRoot, "metadata.db"))
	require.NoError(t, err)
	cacheDir := t.TempDir()
	cm, err := cache.NewManager(cache.Opt{CacheDir: cacheDir})
	require.NoError(t, err)
	fs, err := filesystem.NewFileSystem(context.Background(), filesystem.WithCacheManager(cm))
	require.NoError(t, err)
	s := &snapshotter{
		root: snapshotterRoot,
		ms:   ms,
		fs:   fs,
	}

	// A cached nydus blob layer whose image has no running nydusd, next to a
	// cache file no image refers to.
	txCtx, trans, err := ms.TransactionContext(context.Background(), true)
	require.NoError(t, err)
	_, err = storage.CreateSnapshot(txCtx, snapshots.KindActive, "data-key", "")
	require.NoError(t, err)
	_, err = storage.CommitActive(txCtx, "data-key", "data", snapshots.Usage{}, snapshots.WithLabels(map[string]string{
		label.NydusDataLayer: "true",
		label.CRILayerDigest: "sha256:cached",
	}))
	require.NoError(t, err)
	require.NoError(t, trans.Commit())
	for _, blob := range []string{"cached", "orphan"} {
		require.NoError(t, os.WriteFile(filepath.Join(cacheDir, blob+".blob.data"), nil, 0o644))
	}

	// Only the orphan may go: the cached image's next container reads the
	// other blob without fetching it again.
	unused, err := s.getUnusedCacheBlobs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"orphan"}, unused)
}
