/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"path"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/mount"
	snpkg "github.com/containerd/containerd/pkg/snapshotters"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
)

// `storageLocater` provides a local storage for each handler to save their intermediates.
// Different actions for different layer types
func chooseProcessor(ctx context.Context, logger *logrus.Entry,
	sn *snapshotter, s storage.Snapshot, key, parent string, labels map[string]string,
	storageLocater func() string) (_ func() (bool, []mount.Mount, error), target string, err error) {
	var handler func() (bool, []mount.Mount, error)

	// Handler to prepare a directory for containerd to download and unpacking layer.
	defaultHandler := func() (bool, []mount.Mount, error) {
		mounts, err := sn.mounts(ctx, labels, s)
		return false, mounts, err
	}

	// Handler to stop containerd from downloading and unpacking layer.
	skipHandler := func() (bool, []mount.Mount, error) {
		return true, nil, nil
	}

	remoteHandler := func(id string, labels map[string]string) func() (bool, []mount.Mount, error) {
		return func() (bool, []mount.Mount, error) {
			logger.Debugf("Prepare remote snapshot %s", id)
			if err := sn.prepareRemoteSnapshot(id, labels, s); err != nil {
				return false, nil, err
			}

			// Let Prepare operation show the rootfs content.
			if err := sn.fs.WaitUntilReady(id); err != nil {
				return false, nil, err
			}

			logger.Infof("Nydus remote snapshot %s is ready", id)
			mounts, err := sn.remoteMounts(ctx, s, id)
			return false, mounts, err
		}
	}

	// OCI image is also marked with "containerd.io/snapshot.ref" by Containerd
	target, remote := labels[label.TargetSnapshotRef]

	if remote {
		// Containerd won't consume mount slice for below snapshots
		switch {
		case label.IsNydusMetaLayer(labels):
			logger.Debugf("found nydus meta layer")
			handler = defaultHandler
		case label.IsNydusDataLayer(labels):
			logger.Debugf("found nydus data layer")
			handler = skipHandler
		case sn.fs.CheckReferrer(ctx, labels):
			logger.Debugf("found referenced nydus manifest")
			handler = skipHandler
		default:
			if sn.fs.StargzEnabled() {
				// Check if the blob is format of estargz
				if ok, blob := sn.fs.IsStargzDataLayer(labels); ok {
					err := sn.fs.PrepareStargzMetaLayer(blob, storageLocater(), labels)
					if err != nil {
						logger.Errorf("prepare stargz layer of snapshot ID %s, err: %v", s.ID, err)
					} else {
						logger.Debugf("found estargz data layer")
						// Mark this snapshot as stargz layer since estargz image format does not
						// has special annotation or media type.
						labels[label.StargzLayer] = "true"
						handler = skipHandler
					}
				}
			}
			if handler == nil && sn.fs.TarfsEnabled() {
				err := sn.fs.PrepareTarfsLayer(ctx, labels, s.ID, sn.upperPath(s.ID))
				if err != nil {
					logger.Warnf("snapshot ID %s can't be converted into tarfs, fallback to containerd, err: %v", s.ID, err)
				} else {
					logger.Debugf("convert OCIv1 layer to tarfs")
					labels[label.NydusTarfsLayer] = "true"
					handler = skipHandler
				}
			}
		}
	} else {
		// Container writable layer comes into this branch.
		// It should not be committed during this Prepare() operation.

		// Hope to find bootstrap layer and prepares to start nydusd
		// TODO: Trying find nydus meta layer will slow down setting up rootfs to OCI images
		if id, info, err := sn.findMetaLayer(ctx, key); err == nil {
			logger.Infof("Prepare active Nydus snapshot %s", key)
			handler = remoteHandler(id, info.Labels)
		}

		if handler == nil && sn.fs.ReferrerDetectEnabled() {
			if id, info, err := sn.findReferrerLayer(ctx, key); err == nil {
				logger.Infof("Found referenced nydus manifest for image: %s", info.Labels[snpkg.TargetRefLabel])
				metaPath := path.Join(sn.snapshotDir(id), "fs", "image.boot")
				if err := sn.fs.TryFetchMetadata(ctx, info.Labels, metaPath); err != nil {
					return nil, "", errors.Wrap(err, "try fetch metadata")
				}
				handler = remoteHandler(id, info.Labels)
			}
		}

		if handler == nil && sn.fs.StargzEnabled() {
			// `pInfo` must be the uppermost parent layer
			id, pInfo, _, err := snapshot.GetSnapshotInfo(ctx, sn.ms, parent)
			if err != nil {
				return nil, "", errors.Wrap(err, "get parent snapshot info")
			}

			if sn.fs.StargzLayer(pInfo.Labels) {
				if err := sn.fs.MergeStargzMetaLayer(ctx, s); err != nil {
					return nil, "", errors.Wrap(err, "merge stargz meta layers")
				}
				handler = remoteHandler(id, pInfo.Labels)
				logger.Infof("Generated estargz merged meta for %s", key)
			}
		}

		if handler == nil && sn.fs.TarfsEnabled() {
			// TODO may need check all parrents, in case share layers with other images which already prepared by overlay snapshotter

			// tarfs merged & mounted on the uppermost parent layer
			id, pInfo, _, err := snapshot.GetSnapshotInfo(ctx, sn.ms, parent)
			switch {
			case err != nil:
				logger.Warnf("Tarfs enable but can't get snapshot %s Parent, is an untar oci or nydus snapshot?", s.ID)
			case !label.IsTarfsDataLayer(pInfo.Labels):
				logger.Debugf("Tarfs enable but Parent (%s) of snapshot %s is not a tarfs layer", id, s.ID)
			default:
				err := sn.fs.MergeTarfsLayers(s, func(id string) string { return sn.upperPath(id) })
				if err != nil {
					return nil, "", errors.Wrap(err, "merge tarfs layers")
				}
				handler = remoteHandler(id, pInfo.Labels)
			}
		}
	}

	if handler == nil {
		handler = defaultHandler
	}

	return handler, target, err
}
