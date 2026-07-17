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
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
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
