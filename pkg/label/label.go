/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package label

import (
	"fmt"
	"math"
	"strings"

	"github.com/containerd/containerd/v2/core/snapshots"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/nydus-snapshotter/pkg/utils/parser"
)

// For package compatibility, we still keep the old exported name here.
var AppendLabelsHandlerWrapper = snpkg.AppendInfoHandlerWrapper

// For package compatibility, we still keep the old exported name here.
const (
	CRIImageRef       = snpkg.TargetRefLabel
	CRIImageLayers    = snpkg.TargetImageLayersLabel
	CRILayerDigest    = snpkg.TargetLayerDigestLabel
	CRIManifestDigest = snpkg.TargetManifestDigestLabel
)

const (
	// Marker for remote snapshotter to handle the pull request.
	// During image pull, the containerd client calls Prepare API with the label containerd.io/snapshot.ref.
	// This is a containerd-defined label which contains ChainID that targets a committed snapshot that the
	// client is trying to prepare.
	TargetSnapshotRef = "containerd.io/snapshot.ref"

	// A bool flag to mark the blob as a Nydus data blob, set by image builders.
	NydusDataLayer = "containerd.io/snapshot/nydus-blob"
	// A bool flag to mark the blob as a nydus bootstrap, set by image builders.
	NydusMetaLayer = "containerd.io/snapshot/nydus-bootstrap"
	// The referenced blob sha256 in format of `sha256:xxx`, set by image builders.
	NydusRefLayer = "containerd.io/snapshot/nydus-ref"
	// The blobID of associated layer, also marking the layer as a nydus tarfs, set by the snapshotter
	NydusTarfsLayer = "containerd.io/snapshot/nydus-tarfs"
	// Dm-verity information for image block device
	NydusImageBlockInfo = "containerd.io/snapshot/nydus-image-block"
	// Dm-verity information for layer block device
	NydusLayerBlockInfo = "containerd.io/snapshot/nydus-layer-block"
	// Annotation containing secret to pull images from registry, set by the snapshotter.
	NydusImagePullSecret = "containerd.io/snapshot/pullsecret"
	// Annotation containing username to pull images from registry, set by the snapshotter.
	NydusImagePullUsername = "containerd.io/snapshot/pullusername"
	// Proxy image pull actions to other agents.
	NydusProxyMode = "containerd.io/snapshot/nydus-proxy-mode"
	// A bool flag to enable integrity verification of meta data blob
	NydusSignature = "containerd.io/snapshot/nydus-signature"

	// A bool flag to mark the blob as a estargz data blob, set by the snapshotter.
	StargzLayer = "containerd.io/snapshot/stargz"

	// volatileOpt is a key of an optional label to each snapshot.
	// If this optional label of a snapshot is specified, when mounted to rootdir
	// this snapshot will include volatile option
	OverlayfsVolatileOpt = "containerd.io/snapshot/overlay.volatile"

	// A bool flag to mark it is recommended to run this image with tarfs mode, set by image builders.
	// runtime can decide whether to rely on this annotation
	TarfsHint = "containerd.io/snapshot/tarfs-hint"

	// An alternative nydus index manifest exists in the original OCI index manifest for this snapshot
	NydusIndexAlternative = "containerd.io/snapshot/nydus-index-alternative"

	SnapshotUIDMapping = snapshots.LabelSnapshotUIDMapping

	SnapshotGIDMapping = snapshots.LabelSnapshotGIDMapping
)

// IDMapping represents a contiguous UID/GID mapping tuple (Internal, External, Range),
// matching nydusd's `RafsConfigV2.id_mapping` format.
type IDMapping struct {
	Internal uint32 // User/Group ID inside the container (namespace)
	External uint32 // Mapped User/Group ID on the host
	Range    uint32 // Number of contiguous IDs in the mapping
}

// parseSingleMapping parses one containerd mapping string `internalID:hostID:size`.
// The format maps an in-container (namespace) User/Group ID to a host (external) ID.
func parseSingleMapping(kind, value string) (*IDMapping, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("nydus idmap: invalid %s mapping %q, expected internalID:hostID:size", kind, value)
	}

	internalID, err := parser.ParseUint32(parts[0])
	if err != nil {
		return nil, fmt.Errorf("nydus idmap: invalid %s internalID in %q: %w", kind, value, err)
	}

	externalID, err := parser.ParseUint32(parts[1])
	if err != nil {
		return nil, fmt.Errorf("nydus idmap: invalid %s hostID in %q: %w", kind, value, err)
	}

	size, err := parser.ParseUint32(parts[2])
	if err != nil {
		return nil, fmt.Errorf("nydus idmap: invalid %s size in %q: %w", kind, value, err)
	}

	if size == 0 {
		return nil, fmt.Errorf("nydus idmap: %s mapping %q has zero range", kind, value)
	}

	// Perform arithmetic overflow checks.
	if internalID > math.MaxUint32-size || externalID > math.MaxUint32-size {
		return nil, fmt.Errorf("nydus idmap: %s mapping %q overflows uint32", kind, value)
	}

	return &IDMapping{Internal: internalID, External: externalID, Range: size}, nil
}

// ParseIDMapping extracts raw UID/GID mappings from snapshot labels.
// It enforces that UID and GID mappings are identical and returns a single
// unified IDMapping. Returns nil with no error if idmap labels are absent.
func ParseIDMapping(labels map[string]string) (*IDMapping, error) {
	uidStr := labels[SnapshotUIDMapping]
	gidStr := labels[SnapshotGIDMapping]

	if uidStr == "" && gidStr == "" {
		//nolint:nilnil // intentionally nil, nil: success with no mapping present
		return nil, nil
	}

	if uidStr == "" || gidStr == "" {
		return nil, fmt.Errorf("nydus idmap: both uid and gid mappings must be provided")
	}

	if uidStr != gidStr {
		return nil, fmt.Errorf("nydus idmap: uid mapping %q and gid mapping %q do not match", uidStr, gidStr)
	}

	idMapping, err := parseSingleMapping("uid", uidStr)
	if err != nil {
		return nil, err
	}

	return idMapping, nil
}

func RafsInstanceID(snapshotID string, idMapping *IDMapping) string {
	if idMapping == nil {
		return snapshotID
	}

	return fmt.Sprintf("%s-userns-%d", snapshotID, idMapping.External)
}

func RafsInstanceIDFromLabels(snapshotID string, labels map[string]string) (string, error) {
	idMapping, err := ParseIDMapping(labels)
	if err != nil {
		return "", err
	}

	return RafsInstanceID(snapshotID, idMapping), nil
}

func IsNydusDataLayer(labels map[string]string) bool {
	_, ok := labels[NydusDataLayer]
	return ok
}

func IsNydusMetaLayer(labels map[string]string) bool {
	_, ok := labels[NydusMetaLayer]
	return ok
}

func IsTarfsDataLayer(labels map[string]string) bool {
	_, ok := labels[NydusTarfsLayer]
	return ok
}

func IsNydusProxyMode(labels map[string]string) bool {
	_, ok := labels[NydusProxyMode]
	return ok
}

func HasTarfsHint(labels map[string]string) bool {
	_, ok := labels[TarfsHint]
	return ok
}
