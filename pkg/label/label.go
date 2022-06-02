/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package label

const (
	Signature = "containerd.io/snapshot/nydus-signature"

	CRIImageRef    = "containerd.io/snapshot/cri.image-ref"
	CRIImageLayers = "containerd.io/snapshot/cri.image-layers"
	CRILayerDigest = "containerd.io/snapshot/cri.layer-digest"

	ImagePullSecret   = "containerd.io/snapshot/pullsecret"
	ImagePullUsername = "containerd.io/snapshot/pullusername"

	TargetSnapshotLabel = "containerd.io/snapshot.ref"
	RemoteLabel         = "containerd.io/snapshot/remote"

	NydusMetaLayer = "containerd.io/snapshot/nydus-bootstrap"
	NydusDataLayer = "containerd.io/snapshot/nydus-blob"
)
