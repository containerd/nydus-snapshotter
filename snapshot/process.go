/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"path"

	"github.com/containerd/containerd/mount"
	snpkg "github.com/containerd/containerd/pkg/snapshotters"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// `storageLocater` provides a local storage for each handler to save their intermediates.
// Different actions for different layer types
func chooseProcessor(ctx context.Context, logger *logrus.Entry,
	sn *snapshotter, s storage.Snapshot,
	key, parent string, labels map[string]string, storageLocater func(string) string) (_ func() (bool, []mount.Mount, error), target string, err error) {

	handler := func() (bool, []mount.Mount, error) {
		// Prepare a space for containerd to make snapshot from container image.
		mounts, err := sn.mounts(ctx, labels, s)
		return false, mounts, err
	}

	target, remote := labels[label.TargetSnapshotRef]

	skipHandler := func() (bool, []mount.Mount, error) {
		// The handler tells containerd do not download and unpack layer.
		return true, nil, nil
	}

	if remote {
		// Containerd won't consume mount slice for below snapshots
		switch {
		case label.IsNydusDataLayer(labels):
			logger.Debugf("found nydus data layer")
			handler = skipHandler
		case label.IsNydusMetaLayer(labels):
			// Containerd has to download and unpack nydus meta layer for nydusd
		case sn.fs.CheckReferrer(ctx, labels):
			logger.Debugf("found referenced nydus manifest")
			handler = skipHandler
		case sn.fs.StargzEnabled():
			// Check if the blob is format of estargz
			if ok, blob := sn.fs.IsStargzDataLayer(labels); ok {
				err := sn.fs.PrepareStargzMetaLayer(blob, storageLocater(s.ID), labels)
				if err != nil {
					logger.Errorf("prepare stargz layer of snapshot ID %s, err: %v", s.ID, err)
				} else {
					// Mark this snapshot as stargz layer since estargz image format does not
					// has special annotation or media type.
					labels[label.StargzLayer] = "true"
				}
			}
		case sn.tarfsEnabled():
			ref, ok := labels[snpkg.TargetRefLabel]
			if !ok {
				logger.Errorf("not found image reference lable")
				break
			}
			digestID, ok := labels[snpkg.TargetLayerDigestLabel]
			if !ok {
				logger.Errorf("not found layer digest lable")
				break
			}
			manifest, ok := labels[snpkg.TargetManifestDigestLabel]
			if !ok {
				logger.Errorf("not found manifest digest lable")
				break
			}
			if sn.tarfsManager.IsAsyncFormat {
				go func() {
					err := sn.tarfsManager.PrepareTarfsLayer(s.ID, ref, manifest, digestID, storageLocater(s.ID))
					if err != nil {
						logger.Errorf("async prepare Tarfs layer of snapshot ID %s, err: %v", s.ID, err)
					}
				}()
			} else {
				err := sn.tarfsManager.PrepareTarfsLayer(s.ID, ref, manifest, digestID, storageLocater(s.ID))
				if err != nil {
					logger.Errorf("prepare Tarfs layer of snapshot ID %s, err: %v", s.ID, err)
				}
			}

			handler = func() (bool, []mount.Mount, error) {
				// For tarfs mode , we alread download the content and will format tarfs bootstrap
				// no need for containerd to download and unpack it.
				return true, nil, nil
			}
		default:
			// OCI image is also marked with "containerd.io/snapshot.ref" by Containerd
		}
	} else {
		// Container writable layer comes into this branch. It can't be committed within this Prepare

		remoteHandler := func(id string, labels map[string]string) func() (bool, []mount.Mount, error) {
			return func() (bool, []mount.Mount, error) {
				logger.Debugf("Found nydus meta layer id %s", id)
				if err := sn.prepareRemoteSnapshot(id, labels); err != nil {
					return false, nil, err
				}

				// Let Prepare operation show the rootfs content.
				if err := sn.fs.WaitUntilReady(id); err != nil {
					return false, nil, err
				}

				mounts, err := sn.remoteMounts(ctx, s, id)
				return false, mounts, err
			}
		}

		// Hope to find bootstrap layer and prepares to start nydusd
		// TODO: Trying find nydus meta layer will slow down setting up rootfs to OCI images
		if id, info, err := sn.findMetaLayer(ctx, key); err == nil {
			logger.Infof("Prepares active snapshot %s, nydusd should start afterwards", key)
			handler = remoteHandler(id, info.Labels)
		}

		if sn.fs.ReferrerDetectEnabled() {
			if id, info, err := sn.findReferrerLayer(ctx, key); err == nil {
				logger.Infof("found referenced nydus manifest for image: %s", info.Labels[snpkg.TargetRefLabel])
				metaPath := path.Join(sn.snapshotDir(id), "fs", "image.boot")
				if err := sn.fs.TryFetchMetadata(ctx, info.Labels, metaPath); err != nil {
					return nil, "", errors.Wrap(err, "try fetch metadata")
				}
				handler = remoteHandler(id, info.Labels)
			}
		}

		if sn.fs.StargzEnabled() {
			// `pInfo` must be the uppermost parent layer
			_, pInfo, _, err := snapshot.GetSnapshotInfo(ctx, sn.ms, parent)
			if err != nil {
				return nil, "", errors.Wrap(err, "get parent snapshot info")
			}

			if sn.fs.StargzLayer(pInfo.Labels) {
				if err := sn.fs.MergeStargzMetaLayer(ctx, s); err != nil {
					return nil, "", errors.Wrap(err, "merge stargz meta layers")
				}
			}
		}

		if sn.tarfsEnabled() {
			if len(s.ParentIDs) > 0 {
				if _, err := sn.tarfsManager.GetSnapshotStatus(s.ParentIDs[0]); err == nil {
					logger.Infof("Prepares active snapshot %s, erofs for Tarfs will be mounted on snapshot %s", s.ID, s.ParentIDs[0])
					handler = func() (bool, []mount.Mount, error) {
						err := sn.tarfsManager.MergeTarfsLayers(s, storageLocater)
						if err != nil {
							logger.Errorf("merge Tarfs layer err: %sv", err)
							return false, nil, errors.Wrap(err, "merge tarfs meta layers")
						}
						// mount erofs at uppermost parent snapshot dir
						err = sn.tarfsManager.MountTarErofs(s, sn.tarfsPath(s.ParentIDs[0]))
						if err != nil {
							logger.Errorf("mount erofs Tarfs err: %sv", err)
							return false, nil, errors.Wrap(err, "mount erofs tarfs")
						}
						mount, err := sn.tarfsManager.RemoteMountTarfs(sn.upperPath(s.ID), sn.workPath(s.ID), s.ParentIDs[0])
						return false, mount, err
					}
				} else {
					logger.Warnf("Tarfs enable but snapshot %s ParentIDs[0] not found, is an untar oci or nydus snapshot?", s.ID)
				}
			} else {
				logger.Warnf("Tarfs enable but snapshot %s ParentIDs is empty, is an untar oci or nydus  snapshot?", s.ID)
			}
		}
	}

	return handler, target, err
}
