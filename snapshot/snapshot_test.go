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
