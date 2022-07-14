/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package label

const (
	// Labels defined by CRI
	CRIImageRef       = "containerd.io/snapshot/cri.image-ref"
	CRIImageLayers    = "containerd.io/snapshot/cri.image-layers"
	CRILayerDigest    = "containerd.io/snapshot/cri.layer-digest"
	CRIManifestDigest = "containerd.io/snapshot/cri.manifest-digest"

	// Labels defined by containerd
	TargetSnapshotRef = "containerd.io/snapshot.ref"

	// Labels defined by nydus
	NydusDataLayer         = "containerd.io/snapshot/nydus-blob"
	NydusMetaLayer         = "containerd.io/snapshot/nydus-bootstrap"
	NydusImagePullSecret   = "containerd.io/snapshot/pullsecret"
	NydusImagePullUsername = "containerd.io/snapshot/pullusername"
	NydusSignature         = "containerd.io/snapshot/nydus-signature"

	// Labels defined by estargz
	StargzLayer = "containerd.io/snapshot/stargz"

	// volatileOpt is a key of an optional lablel to each snapshot.
	// If this optional label of a snapshot is specified, when mounted to rootdir
	// this snapshot will include volatile option
	VolatileOpt = "containerd.io/snapshot/overlay.volatile"
)
