/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
)

// `storageLocater` provides a local storage for each handler to save their intermediates.
// Different actions for different layer types
func chooseProcessor(ctx context.Context, logger *logrus.Entry,
	sn *snapshotter, s storage.Snapshot,
	key, parent string, labels map[string]string, storageLocater func() string) (_ func() (bool, []mount.Mount, error), target string, err error) {

	var handler func() (bool, []mount.Mount, error) = func() (bool, []mount.Mount, error) {
		// Prepare a space for containerd to make snapshot from container image.
		mounts, err := sn.mounts(ctx, labels, s)
		return false, mounts, err
	}

	target, remote := labels[label.TargetSnapshotRef]

	if remote {
		// Containerd won't consume mount slice for below snapshots
		switch {
		case isNydusDataLayer(labels):
			handler = func() (bool, []mount.Mount, error) {
				// For nydus data layer, we can't return mount slice since nydusd does
				// not start yet and there is no need for containerd to download and unpack it.
				return true, nil, nil
			}
		case isNydusMetaLayer(labels):
			// Containerd has to download and unpack nydus meta layer for nydusd
		case sn.fs.StargzEnabled():
			// Check if the blob is format of estargz
			if ok, blob := sn.fs.IsStargzDataLayer(labels); ok {
				err := sn.fs.PrepareStargzMetaLayer(blob, storageLocater(), labels)
				if err != nil {
					logger.Errorf("prepare stargz layer of snapshot ID %s, err: %v", s.ID, err)
				} else {
					// Mark this snapshot as stargz layer since estargz image format does not
					// has special annotation or media-type.
					labels[label.StargzLayer] = "true"
				}
			}
		default:
			// OCI image is also marked with "containerd.io/snapshot.ref" by Containerd
		}
	} else {
		// Container writable layer comes into this branch. It can't be committed within this Prepare

		// Hope to find bootstrap layer and prepares to start nydusd
		// TODO: Trying find nydus meta layer will slow down setting up rootfs to OCI images
		if id, info, err := sn.findMetaLayer(ctx, key); err == nil {
			logger.Infof("Prepares active snapshot %s, nydusd should start afterwards", key)
			handler = func() (bool, []mount.Mount, error) {
				logger.Debugf("Found nydus meta layer id %s", id)
				if err := sn.prepareRemoteSnapshot(id, info.Labels); err != nil {
					return false, nil, err
				}
				// FIXME: What's strange is that we are providing meta snapshot
				// contents but not wait for it reaching RUNNING
				mounts, err := sn.remoteMounts(ctx, s, id)
				return false, mounts, err
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
	}

	return handler, target, err
}
