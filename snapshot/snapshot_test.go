/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/label"
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

func TestMountNativeIDMapping(t *testing.T) {
	snapshotterRoot := "/var/lib/containerd/snapshotter"
	// idmapMountsSupported=true is required for uidmap=/gidmap= options to be emitted.
	s := &snapshotter{
		root:                 snapshotterRoot,
		idmapMountsSupported: true,
	}

	const (
		uidMap = "0:1000:65536"
		gidMap = "0:2000:65536"
	)

	tests := []struct {
		name           string
		snapshot       storage.Snapshot
		labels         map[string]string
		expectedMounts []mount.Mount
	}{
		{
			name: "no parents, active snapshot - bind mount carries uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "idmap1",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			expectedMounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  s.upperPath("idmap1"),
					Options: []string{"uidmap=" + uidMap, "gidmap=" + gidMap, "rw", "rbind"},
				},
			},
		},
		{
			name: "no parents, view snapshot - ro bind mount carries uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "idmap2",
				Kind:      snapshots.KindView,
				ParentIDs: []string{},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			expectedMounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  s.upperPath("idmap2"),
					Options: []string{"uidmap=" + uidMap, "gidmap=" + gidMap, "ro", "rbind"},
				},
			},
		},
		{
			name: "one parent - ro bind mount carries uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "idmap3",
				Kind:      snapshots.KindView,
				ParentIDs: []string{"parentA"},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			expectedMounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  s.upperPath("parentA"),
					Options: []string{"uidmap=" + uidMap, "gidmap=" + gidMap, "ro", "rbind"},
				},
			},
		},
		{
			name: "multiple parents, active snapshot - overlay mount carries uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "idmap4",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{"parentA", "parentB"},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"uidmap=" + uidMap,
						"gidmap=" + gidMap,
						fmt.Sprintf("workdir=%s", s.workPath("idmap4")),
						fmt.Sprintf("upperdir=%s", s.upperPath("idmap4")),
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parentA"), s.upperPath("parentB")),
					},
				},
			},
		},
		{
			name: "multiple parents, active snapshot - IDMapping and volatile together",
			snapshot: storage.Snapshot{
				ID:        "idmap5",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{"parentA", "parentB"},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
				label.OverlayfsVolatileOpt:    "true",
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"uidmap=" + uidMap,
						"gidmap=" + gidMap,
						fmt.Sprintf("workdir=%s", s.workPath("idmap5")),
						fmt.Sprintf("upperdir=%s", s.upperPath("idmap5")),
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parentA"), s.upperPath("parentB")),
						"volatile",
					},
				},
			},
		},
		{
			name: "multiple parents, view snapshot - overlay lowerdir carries uidmap/gidmap",
			snapshot: storage.Snapshot{
				ID:        "idmap6",
				Kind:      snapshots.KindView,
				ParentIDs: []string{"parentA", "parentB"},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			expectedMounts: []mount.Mount{
				{
					Type:   "overlay",
					Source: "overlay",
					Options: []string{
						"uidmap=" + uidMap,
						"gidmap=" + gidMap,
						fmt.Sprintf("lowerdir=%s:%s", s.upperPath("parentA"), s.upperPath("parentB")),
					},
				},
			},
		},
		{
			name: "IDMapping with only UID mapping - only uidmap option emitted",
			snapshot: storage.Snapshot{
				ID:        "idmap7",
				Kind:      snapshots.KindActive,
				ParentIDs: []string{},
			},
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				// no GID mapping
			},
			expectedMounts: []mount.Mount{
				{
					Type:    "bind",
					Source:  s.upperPath("idmap7"),
					Options: []string{"uidmap=" + uidMap, "rw", "rbind"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mounts, err := s.mountNative(ctx, tt.labels, tt.snapshot)
			require.NoError(t, err)
			require.Len(t, mounts, len(tt.expectedMounts))
			for i, want := range tt.expectedMounts {
				assert.Equal(t, want.Type, mounts[i].Type)
				assert.Equal(t, want.Source, mounts[i].Source)
				assert.ElementsMatch(t, want.Options, mounts[i].Options)
			}
		})
	}
}

// TestMountNativeIDMappingKernelUnsupported verifies that no uidmap=/gidmap= options
// are emitted when idmapMountsSupported=false, even if IDMapping labels are present.
func TestMountNativeIDMappingKernelUnsupported(t *testing.T) {
	snapshotterRoot := "/var/lib/containerd/snapshotter"
	// idmapMountsSupported=false (zero value) — no uidmap/gidmap options emitted.
	s := &snapshotter{
		root:                 snapshotterRoot,
		idmapMountsSupported: false,
	}

	const (
		uidMap = "0:1000:65536"
		gidMap = "0:2000:65536"
	)

	labels := map[string]string{
		label.LabelSnapshotUIDMapping: uidMap,
		label.LabelSnapshotGIDMapping: gidMap,
	}

	ctx := context.Background()

	t.Run("bind mount (no parents) - no uidmap/gidmap when kernel unsupported", func(t *testing.T) {
		snap := storage.Snapshot{ID: "u1", Kind: snapshots.KindActive, ParentIDs: []string{}}
		mounts, err := s.mountNative(ctx, labels, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		opts := strings.Join(mounts[0].Options, " ")
		assert.NotContains(t, opts, "uidmap=")
		assert.NotContains(t, opts, "gidmap=")
	})

	t.Run("single parent bind mount - no uidmap/gidmap when kernel unsupported", func(t *testing.T) {
		snap := storage.Snapshot{ID: "u2", Kind: snapshots.KindView, ParentIDs: []string{"parentA"}}
		mounts, err := s.mountNative(ctx, labels, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		opts := strings.Join(mounts[0].Options, " ")
		assert.NotContains(t, opts, "uidmap=")
		assert.NotContains(t, opts, "gidmap=")
	})

	t.Run("overlay mount - no uidmap/gidmap when kernel unsupported", func(t *testing.T) {
		snap := storage.Snapshot{ID: "u3", Kind: snapshots.KindActive, ParentIDs: []string{"parentA", "parentB"}}
		mounts, err := s.mountNative(ctx, labels, snap)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		opts := strings.Join(mounts[0].Options, " ")
		assert.NotContains(t, opts, "uidmap=")
		assert.NotContains(t, opts, "gidmap=")
	})
}

// TestMountRemoteIDMappingOptions verifies the IDMapping option-building logic for
// mountRemote: uidmap=/gidmap= are only emitted when idmapMountsSupported=true and
// only for the plain kernel overlay path (fuse.nydus-overlayfs and Kata do not support
// them). mountRemote requires a live Filesystem, so only the option logic is tested.
func TestMountRemoteIDMappingOptions(t *testing.T) {
	const (
		uidMap = "0:1000:65536"
		gidMap = "0:2000:65536"
	)

	// buildOpts mirrors the final step of mountRemote where idmap options are prepended
	// before calling overlayMount, gated on idmapSupported.
	buildOpts := func(labels map[string]string, idmapSupported bool) []string {
		var opts []string
		if _, ok := labels[label.OverlayfsVolatileOpt]; ok {
			opts = append(opts, "volatile")
		}
		if idmapSupported {
			if v, ok := labels[label.LabelSnapshotUIDMapping]; ok {
				opts = append([]string{fmt.Sprintf("uidmap=%s", v)}, opts...)
			}
			if v, ok := labels[label.LabelSnapshotGIDMapping]; ok {
				opts = append([]string{fmt.Sprintf("gidmap=%s", v)}, opts...)
			}
		}
		return opts
	}

	tests := []struct {
		name           string
		labels         map[string]string
		idmapSupported bool
		wantContains   []string
		wantAbsent     []string
	}{
		{
			name: "kernel supported + IDMapping labels produce uidmap and gidmap",
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			idmapSupported: true,
			wantContains:   []string{"uidmap=" + uidMap, "gidmap=" + gidMap},
		},
		{
			name: "kernel unsupported - IDMapping labels are ignored",
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
			},
			idmapSupported: false,
			wantAbsent:     []string{"uidmap=", "gidmap="},
		},
		{
			name:           "no IDMapping labels produce no uidmap/gidmap options",
			labels:         map[string]string{},
			idmapSupported: true,
			wantAbsent:     []string{"uidmap=", "gidmap="},
		},
		{
			name: "kernel supported + IDMapping and volatile labels coexist",
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
				label.LabelSnapshotGIDMapping: gidMap,
				label.OverlayfsVolatileOpt:    "true",
			},
			idmapSupported: true,
			wantContains:   []string{"uidmap=" + uidMap, "gidmap=" + gidMap, "volatile"},
		},
		{
			name: "kernel supported + only UID mapping - no gidmap emitted",
			labels: map[string]string{
				label.LabelSnapshotUIDMapping: uidMap,
			},
			idmapSupported: true,
			wantContains:   []string{"uidmap=" + uidMap},
			wantAbsent:     []string{"gidmap="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := buildOpts(tt.labels, tt.idmapSupported)
			joined := strings.Join(opts, " ")
			for _, want := range tt.wantContains {
				assert.Contains(t, joined, want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, joined, absent)
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
