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

	TargetSnapshotLabel       = "containerd.io/snapshot.ref"
	TargetManifestDigestLabel = "containerd.io/snapshot/cri.manifest-digest"
	StargzLayer               = "containerd.io/snapshot/stargz"

	NydusMetaLayer = "containerd.io/snapshot/nydus-bootstrap"
	NydusDataLayer = "containerd.io/snapshot/nydus-blob"

	// volatileOpt is a key of an optional lablel to each snapshot.
	// If this optional label of a snapshot is specified, when mounted to rootdir
	// this snapshot will include volatile option
	VolatileOpt = "containerd.io/snapshot/overlay.volatile"
)
