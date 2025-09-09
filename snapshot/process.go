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

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/nydus-snapshotter/config"
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
		mounts, err := sn.mountNative(ctx, labels, s)
		return false, mounts, err
	}

	// Handler to stop containerd from downloading and unpacking layer.
	skipHandler := func() (bool, []mount.Mount, error) {
		return true, nil, nil
	}

	remoteHandler := func(id string, labels map[string]string) func() (bool, []mount.Mount, error) {
		return func() (bool, []mount.Mount, error) {
			logger.Debugf("Prepare remote snapshot %s", id)
			if err := sn.fs.Mount(ctx, id, labels, &s); err != nil {
				return false, nil, err
			}

			// Let Prepare operation show the rootfs content.
			if err := sn.fs.WaitUntilReady(id); err != nil {
				return false, nil, err
			}

			logger.Infof("Nydus remote snapshot %s is ready", id)
			mounts, err := sn.mountRemote(ctx, labels, s, id, key)
			return false, mounts, err
		}
	}

	proxyHandler := func() (bool, []mount.Mount, error) {
		mounts, err := sn.mountProxy(ctx, s)
		return false, mounts, err
	}

	// OCI image is also marked with "containerd.io/snapshot.ref" by Containerd
	target, isRoLayer := labels[label.TargetSnapshotRef]

	if isRoLayer {
		// Containerd won't consume mount slice for below snapshots
		switch {
		case config.GetFsDriver() == config.FsDriverProxy:
			logger.Debugf("proxy image pull request to other agents")
			if ref := labels[label.CRILayerDigest]; len(ref) > 0 {
				labels[label.NydusProxyMode] = "true"
				handler = skipHandler
			} else {
				return nil, "", errors.Errorf("missing CRI reference annotation for snapshot %s", s.ID)
			}
		case label.IsNydusMetaLayer(labels):
			logger.Debugf("found nydus meta layer")
			handler = defaultHandler
		case label.IsNydusDataLayer(labels):
			logger.Debugf("found nydus data layer")
			handler = skipHandler
		case sn.fs.CheckIndexAlternative(ctx, labels):
			logger.Debugf("found nydus alternative image in index")
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
				logger.Debugf("convert OCIv1 layer to tarfs")
				err := sn.fs.PrepareTarfsLayer(ctx, labels, s.ID, sn.upperPath(s.ID))
				if err != nil {
					logger.Warnf("snapshot ID %s can't be converted into tarfs, fallback to containerd, err: %v", s.ID, err)
				} else {
					if config.GetTarfsExportEnabled() {
						_, err = sn.fs.ExportBlockData(s, true, labels, func(id string) string { return sn.upperPath(id) })
						if err != nil {
							return nil, "", errors.Wrap(err, "export layer as tarfs block device")
						}
					}
					handler = skipHandler
				}
			}
		}
	} else {
		// Container writable layer comes into this branch.
		// It should not be committed during this Prepare() operation.

		pID, pInfo, _, pErr := snapshot.GetSnapshotInfo(ctx, sn.ms, parent)
		if treatAsProxyDriver(pInfo.Labels) {
			logger.Warnf("treat as proxy mode for the prepared snapshot by other snapshotter possibly: id = %s, labels = %v", pID, pInfo.Labels)
			handler = proxyHandler
		}
		if pErr == nil && label.IsNydusProxyMode(pInfo.Labels) {
			logger.Infof("Prepare active snapshot %s in proxy mode", key)
			handler = remoteHandler(pID, pInfo.Labels)
		}

		// Hope to find bootstrap layer and prepares to start nydusd
		// TODO: Trying find nydus meta layer will slow down setting up rootfs to OCI images
		if handler == nil {
			if id, info, err := sn.findMetaLayer(ctx, key); err == nil {
				logger.Infof("Prepare active Nydus snapshot %s", key)
				handler = remoteHandler(id, info.Labels)
			}
		}

		if handler == nil && sn.fs.IndexDetectEnabled() {
			if id, info, err := sn.findIndexAlternativeLayer(ctx, key); err == nil {
				logger.Infof("Found nydus alternative image in index for image: %s", info.Labels[snpkg.TargetRefLabel])
				metaPath := path.Join(sn.snapshotDir(id), "fs", "image.boot")
				if err := sn.fs.TryFetchMetadataFromIndex(ctx, info.Labels, metaPath); err != nil {
					return nil, "", errors.Wrap(err, "try fetch metadata")
				}
				handler = remoteHandler(id, info.Labels)
			}
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

		if handler == nil && pErr == nil && sn.fs.StargzEnabled() && sn.fs.StargzLayer(pInfo.Labels) {
			if err := sn.fs.MergeStargzMetaLayer(ctx, s); err != nil {
				return nil, "", errors.Wrap(err, "merge stargz meta layers")
			}
			handler = remoteHandler(pID, pInfo.Labels)
			logger.Infof("Generated estargz merged meta for %s", key)
		}

		if handler == nil && pErr == nil && sn.fs.TarfsEnabled() && label.IsTarfsDataLayer(pInfo.Labels) {
			// Merge and mount tarfs on the uppermost parent layer.
			// TODO may need to check all parrent layers, in case share layers with other images
			// which have already been prepared by overlay snapshotter

			logger.Infof("Prepare active snapshot %s in Nydus tarfs mode", key)
			err = sn.mergeTarfs(ctx, s, pID, pInfo)
			if err != nil {
				return nil, "", errors.Wrapf(err, "merge tarfs layers for snapshot %s", pID)
			}
			logger.Infof("Prepared active snapshot %s in Nydus tarfs mode", key)
			handler = remoteHandler(pID, pInfo.Labels)
		}
	}

	if handler == nil {
		handler = defaultHandler
	}

	return handler, target, err
}
