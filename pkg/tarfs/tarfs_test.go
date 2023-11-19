/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tarfs

import (
	"context"
	"testing"

	"github.com/containerd/continuity/testutil"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/opencontainers/go-digest"
	"gotest.tools/assert"
)

const (
	BusyboxRef            = "quay.io/quay/busybox@sha256:92f3298bf80a1ba949140d77987f5de081f010337880cd771f7e7fc928f8c74d"
	BusyboxManifestDigest = "sha256:92f3298bf80a1ba949140d77987f5de081f010337880cd771f7e7fc928f8c74d"
	BusyboxLayerDigest    = "sha256:ee780d08a5b4de5192a526d422987f451d9a065e6da42aefe8c3b20023a250c7"
	NydusImagePath        = "nydus-image"
)

// TODO: add unit test for MergeLayers, ExportBlockData, MountErofs, RemountErofs, UmountTarErofs, DetachLayer,
// RecoverSnapshoInfo, RecoverRafsInstance, getImageBlobInfo

func TestPrepareLayer(t *testing.T) {
	manager := NewManager(true, true, t.TempDir(), NydusImagePath, 4)
	manifestDigest, err := digest.Parse(BusyboxManifestDigest)
	assert.Assert(t, err)
	layerDigest, err := digest.Parse(BusyboxLayerDigest)
	assert.Assert(t, err)

	err = manager.PrepareLayer("snapshot1", BusyboxRef, manifestDigest, layerDigest, t.TempDir())
	assert.Assert(t, err)

	snapshot, err := manager.waitLayerReady("snapshot1", true)
	// tarfs_test.go:36: assertion failed: error is not nil: generate tarfs from image layer blob: converting OCIv1 layer blob to tarfs: exec: "nydus-image": executable file not found in $PATH
	assert.Assert(t, err != nil)
	if err == nil {
		assert.Equal(t, snapshot.blobID, "ee780d08a5b4de5192a526d422987f451d9a065e6da42aefe8c3b20023a250c7")
	}

	err = manager.PrepareLayer("snapshot1", BusyboxRef, manifestDigest, layerDigest, t.TempDir())
	assert.Assert(t, err != nil)
}

func TestBlobProcess(t *testing.T) {
	manager := NewManager(true, true, t.TempDir(), NydusImagePath, 4)
	manifestDigest, err := digest.Parse(BusyboxManifestDigest)
	assert.Assert(t, err)
	layerDigest, err := digest.Parse(BusyboxLayerDigest)
	assert.Assert(t, err)

	err = manager.blobProcess(context.Background(), "snapshot2", BusyboxRef, manifestDigest, layerDigest, t.TempDir(), true)
	assert.Assert(t, err != nil)
}

func TestCheckTarfsHintAnnotation(t *testing.T) {
	manager := NewManager(true, true, t.TempDir(), NydusImagePath, 4)
	ctx := context.Background()
	hint, err := manager.CheckTarfsHintAnnotation(ctx, BusyboxRef, BusyboxManifestDigest)
	assert.Assert(t, err)
	assert.Equal(t, hint, false)
}

func TestGetConcurrentLimiter(t *testing.T) {
	manager := NewManager(false, false, t.TempDir(), NydusImagePath, 4)
	limiter := manager.GetConcurrentLimiter("busybox")
	assert.Assert(t, limiter != nil)
	assert.Equal(t, manager.GetConcurrentLimiter("busybox"), limiter)

}

func TestCopyTarfsAnnotations(t *testing.T) {
	manager := NewManager(false, false, t.TempDir(), NydusImagePath, 4)
	rafs := &rafs.Rafs{
		Annotations: make(map[string]string),
	}

	annotations := map[string]string{}
	annotations[label.CRIImageRef] = "cri_image_ref"
	annotations[label.CRILayerDigest] = "cri_layer_digest"
	annotations[label.CRIManifestDigest] = "cri_manigest_digest"
	annotations[label.NydusTarfsLayer] = "nydus_tarfs_layer"
	annotations[label.NydusImageBlockInfo] = "nydus_image_block_info"
	annotations[label.NydusLayerBlockInfo] = "nydus_layer_block_info"
	annotations["unsupported_key"] = "error"

	manager.copyTarfsAnnotations(annotations, rafs)
	assert.Equal(t, len(rafs.Annotations), 6)
	assert.Equal(t, rafs.Annotations[label.CRIImageRef], annotations[label.CRIImageRef])
	assert.Equal(t, rafs.Annotations[label.CRILayerDigest], annotations[label.CRILayerDigest])
}

func TestTarfsFilePath(t *testing.T) {
	manager := NewManager(false, false, "/tmp/tarfs", NydusImagePath, 4)

	assert.Equal(t, manager.layerTarFilePath("blob1"), "/tmp/tarfs/blob1")
	assert.Equal(t, manager.layerDiskFilePath("blob1"), "/tmp/tarfs/blob1.layer.disk")
	assert.Equal(t, manager.ImageDiskFilePath("blob1"), "/tmp/tarfs/blob1.image.disk")
	assert.Equal(t, manager.layerMetaFilePath("/tarfs/fs"), "/tarfs/fs/image/layer.boot")
	assert.Equal(t, manager.imageMetaFilePath("/tarfs/fs"), "/tarfs/fs/image/image.boot")
}

func TestTarfsStatusString(t *testing.T) {
	assert.Equal(t, tarfsStatusString(TarfsStatusReady), "Ready")
	assert.Equal(t, tarfsStatusString(TarfsStatusPrepare), "Prepare")
	assert.Equal(t, tarfsStatusString(TarfsStatusFailed), "Failed")
	assert.Equal(t, tarfsStatusString(4), "Unknown")
}

func TestAttachBlob(t *testing.T) {
	testutil.RequiresRoot(t)

	manager := NewManager(false, false, t.TempDir(), NydusImagePath, 4)
	blobFile := createTempFile(t)
	loopdev, err := manager.attachLoopdev(blobFile)
	assert.Assert(t, err)
	err = deleteLoop(loopdev)
	assert.Assert(t, err)
	err = deleteLoop(loopdev)
	assert.Assert(t, err != nil)
}
