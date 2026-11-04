/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/index"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestRemoteSnapshotLabels(t *testing.T) {
	remote := map[string]string{
		snpkg.TargetRefLabel:     "example.com/image:latest",
		label.SnapshotUIDMapping: "0:300000:65536",
		label.SnapshotGIDMapping: "0:300000:65536",
	}
	original := maps.Clone(remote)
	for _, tc := range []struct {
		name    string
		mapping string
		wantID  string
	}{
		{"first container", "0:100000:65536", "image-userns-100000"},
		{"second container", "0:200000:65536", "image-userns-200000"},
		{"without user namespace", "", "image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active := map[string]string{"active-only": "not image metadata"}
			if tc.mapping != "" {
				active[label.SnapshotUIDMapping] = tc.mapping
				active[label.SnapshotGIDMapping] = tc.mapping
			}
			before := maps.Clone(active)
			merged := remoteSnapshotLabels(remote, active)
			require.Equal(t, remote[snpkg.TargetRefLabel], merged[snpkg.TargetRefLabel])
			require.NotContains(t, merged, "active-only")
			prepareID, err := label.RafsInstanceIDFromLabels("image", merged)
			require.NoError(t, err)
			mountsID, err := label.RafsInstanceIDFromLabels("image", active)
			require.NoError(t, err)
			require.Equal(t, tc.wantID, prepareID)
			require.Equal(t, mountsID, prepareID)
			merged[snpkg.TargetRefLabel] = "changed"
			require.Equal(t, original, remote)
			require.Equal(t, before, active)
		})
	}

	// An inherited GID must not disguise an incomplete active mapping.
	merged := remoteSnapshotLabels(remote, map[string]string{label.SnapshotUIDMapping: "0:300000:65536"})
	_, err := label.RafsInstanceIDFromLabels("image", merged)
	require.ErrorContains(t, err, "both uid and gid mappings must be provided")
	require.Empty(t, remoteSnapshotLabels(nil, nil))
}

func TestChooseProcessorRemoteMapping(t *testing.T) {
	require.NoError(t, config.ProcessConfigurations(&config.SnapshotterConfig{
		DaemonMode:   string(config.DaemonModeDedicated),
		DaemonConfig: config.DaemonConfig{FsDriver: config.FsDriverFusedev},
	}))
	originalInstances := rafs.RafsGlobalCache.List()
	rafs.RafsGlobalCache.SetIntances(make(map[string]*rafs.Rafs))
	t.Cleanup(func() { rafs.RafsGlobalCache.SetIntances(originalInstances) })
	for _, imageKind := range []string{"direct", "index alternative"} {
		t.Run(imageKind, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			ms, err := storage.NewMetaStore(filepath.Join(root, "metadata.db"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, ms.Close()) })
			fs := &filesystem.Filesystem{}
			remote := map[string]string{
				snpkg.TargetRefLabel:            "example.com/image:latest",
				snpkg.TargetManifestDigestLabel: digest.FromString("manifest").String(),
			}
			if imageKind == "direct" {
				remote[label.NydusMetaLayer] = "true"
			} else {
				remote[label.NydusIndexAlternative] = "true"
				require.NoError(t, filesystem.WithIndexManager(index.NewManager(false))(fs))
			}
			sn := &snapshotter{root: root, ms: ms, fs: fs}
			txCtx, tx, err := ms.TransactionContext(ctx, true)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = storage.CreateSnapshot(txCtx, snapshots.KindActive, "image-unpack", "")
			require.NoError(t, err)
			id, err := storage.CommitActive(txCtx, "image-unpack", "image", snapshots.Usage{}, snapshots.WithLabels(remote))
			require.NoError(t, err)
			require.NoError(t, tx.Commit())

			// An existing bootstrap avoids registry access when selecting the
			// index alternative; no daemon or kernel mount is needed here.
			bootstrap := filepath.Join(root, "snapshots", id, "fs", "image.boot")
			require.NoError(t, os.MkdirAll(filepath.Dir(bootstrap), 0o755))
			require.NoError(t, os.WriteFile(bootstrap, nil, 0o600))

			for _, tc := range []struct {
				name   string
				labels map[string]string
				want   string
			}{
				{"first container", map[string]string{label.SnapshotUIDMapping: "0:100000:65536", label.SnapshotGIDMapping: "0:100000:65536"}, ""},
				{"second container", map[string]string{label.SnapshotUIDMapping: "0:200000:65536", label.SnapshotGIDMapping: "0:200000:65536"}, ""},
				{"without user namespace", nil, ""},
				{"missing gid", map[string]string{label.SnapshotUIDMapping: "0:100000:65536"}, "both uid and gid mappings must be provided"},
				{"different gid", map[string]string{label.SnapshotUIDMapping: "0:100000:65536", label.SnapshotGIDMapping: "0:200000:65536"}, "do not match"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					key := "container-" + tc.name
					txCtx, tx, err := ms.TransactionContext(ctx, true)
					require.NoError(t, err)
					defer func() { _ = tx.Rollback() }()
					active, err := storage.CreateSnapshot(txCtx, snapshots.KindActive, key, "image", snapshots.WithLabels(tc.labels))
					require.NoError(t, err)
					require.NoError(t, tx.Commit())
					handler, _, _, err := chooseProcessor(ctx, logrus.NewEntry(logrus.New()), sn, active, key, "image", tc.labels, func() string { return root })
					require.NoError(t, err)
					if tc.want != "" {
						_, _, err = handler()
						// Invalid mappings must reach fs.Mount's validation, rather
						// than silently preparing an unmapped image instance.
						require.ErrorContains(t, err, tc.want)
						return
					}

					instanceID, err := label.RafsInstanceIDFromLabels(id, tc.labels)
					require.NoError(t, err)
					mountpoint := filepath.Join(root, "mnt", instanceID)
					// Seed a ready instance to exercise mount selection without
					// starting a FUSE daemon. A test driver skips daemon polling.
					rafs.RafsGlobalCache.Add(&rafs.Rafs{SnapshotID: instanceID, Mountpoint: mountpoint, FsDriver: "test"})
					_, prepared, err := handler()
					require.NoError(t, err)
					mounted, err := sn.Mounts(ctx, key)
					require.NoError(t, err)
					require.Equal(t, mounted, prepared)
					require.Len(t, prepared, 1)
					require.Contains(t, prepared[0].Options, "lowerdir="+mountpoint)
				})
			}
		})
	}
}
