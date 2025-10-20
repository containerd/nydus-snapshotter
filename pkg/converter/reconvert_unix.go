//go:build !windows
// +build !windows

/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	containerdConverter "github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// DefaultIndexConvertFunc implements the platform-specific reconversion logic.
func DefaultIndexConvertFunc(layerConvertFunc containerdConverter.ConvertFunc, docker2oci bool, platformMC platforms.MatchComparer) containerdConverter.ConvertFunc {
	hooks := containerdConverter.ConvertHooks{
		PostConvertHook: ReconvertHookFunc(),
	}
	return containerdConverter.IndexConvertFuncWithHook(layerConvertFunc, docker2oci, platformMC, hooks)
}

// ReconvertHookFunc implements the platform-specific hook logic.
func ReconvertHookFunc() containerdConverter.ConvertHookFunc {
	return func(ctx context.Context, cs content.Store, _ ocispec.Descriptor, newDesc *ocispec.Descriptor) (*ocispec.Descriptor, error) {
		// If no conversion happened, return nil to indicate no processing needed
		if newDesc == nil {
			// nolint:nilnil
			return nil, nil
		}

		if !images.IsManifestType(newDesc.MediaType) && !images.IsIndexType(newDesc.MediaType) {
			return newDesc, nil
		}

		if images.IsManifestType(newDesc.MediaType) {
			return processManifest(ctx, cs, *newDesc)
		}

		return newDesc, nil
	}
}

// processManifest removes Nydus bootstrap layers from manifest and updates config
func processManifest(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var manifest ocispec.Manifest
	labels, err := readJSON(ctx, cs, &manifest, desc)
	if err != nil {
		return nil, errors.Wrap(err, "read manifest")
	}

	modified := false

	// Step 1: Remove Nydus bootstrap layers from manifest.Layers
	var bootstrapIndices []int
	for i, layer := range manifest.Layers {
		if IsNydusBootstrap(layer) {
			bootstrapIndices = append(bootstrapIndices, i)
			modified = true
		}
	}

	// Remove bootstrap layers (in reverse order to maintain indices)
	for i := len(bootstrapIndices) - 1; i >= 0; i-- {
		idx := bootstrapIndices[i]
		containerdConverter.ClearGCLabels(labels, manifest.Layers[idx].Digest)
		manifest.Layers = slices.Delete(manifest.Layers, idx, idx+1)
	}

	// Step 2: Update config to remove bootstrap-related diffIDs and history
	if modified {
		newConfigDesc, err := processConfig(ctx, cs, manifest.Config)
		if err != nil {
			return nil, errors.Wrap(err, "process config")
		}
		if newConfigDesc != nil {
			containerdConverter.ClearGCLabels(labels, manifest.Config.Digest)
			labels["containerd.io/gc.ref.content.config"] = newConfigDesc.Digest.String()
			manifest.Config = *newConfigDesc
		}

		// Re-index layer GC labels after bootstrap removal
		for i, layer := range manifest.Layers {
			labelKey := fmt.Sprintf("containerd.io/gc.ref.content.l.%d", i)
			labels[labelKey] = layer.Digest.String()
		}

		return writeJSON(ctx, cs, &manifest, desc, labels)
	}

	return &desc, nil
}

// processConfig removes Nydus bootstrap-related entries from image config
func processConfig(ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var config ocispec.Image
	labels, err := readJSON(ctx, cs, &config, configDesc)
	if err != nil {
		return nil, errors.Wrap(err, "read config")
	}

	modified := false

	// Count bootstrap history entries before removal
	bootstrapHistoryCount := 0
	for _, h := range config.History {
		if h.Comment == "Nydus Bootstrap Layer" && h.CreatedBy == "Nydus Converter" {
			bootstrapHistoryCount++
		}
	}

	// Remove Nydus Bootstrap Layer from history
	for i := len(config.History) - 1; i >= 0; i-- {
		h := config.History[i]
		if h.Comment == "Nydus Bootstrap Layer" && h.CreatedBy == "Nydus Converter" {
			config.History = slices.Delete(config.History, i, i+1)
			modified = true
		}
	}

	// Remove empty layer history entries
	for i := len(config.History) - 1; i >= 0; i-- {
		if config.History[i].EmptyLayer {
			config.History = slices.Delete(config.History, i, i+1)
			modified = true
		}
	}

	// Remove bootstrap diffIDs from RootFS.DiffIDs
	// The number of diffIDs to remove should match the number of bootstrap history entries removed
	if bootstrapHistoryCount > 0 && len(config.RootFS.DiffIDs) >= bootstrapHistoryCount {
		// Remove the last bootstrapHistoryCount diffIDs (bootstrap layers are typically at the end)
		config.RootFS.DiffIDs = config.RootFS.DiffIDs[:len(config.RootFS.DiffIDs)-bootstrapHistoryCount]
		modified = true
	}

	// Handle excessive non-empty layers in History section
	if len(config.RootFS.DiffIDs) < len(config.History) {
		nonEmptyCount := 0
		for _, h := range config.History {
			if !h.EmptyLayer {
				nonEmptyCount++
			}
		}
		if nonEmptyCount != len(config.RootFS.DiffIDs) {
			config.History = []ocispec.History{}
			modified = true
		}
	}

	if modified {
		// Use DualConfig to preserve unknown fields
		var dualConfig map[string]*json.RawMessage
		configBytes, err := content.ReadBlob(ctx, cs, configDesc)
		if err != nil {
			return nil, errors.Wrap(err, "read config blob")
		}
		if err := json.Unmarshal(configBytes, &dualConfig); err != nil {
			return nil, errors.Wrap(err, "unmarshal config to DualConfig")
		}

		// Update rootfs
		rootfsBytes, err := json.Marshal(config.RootFS)
		if err != nil {
			return nil, errors.Wrap(err, "marshal rootfs")
		}
		rootfsRaw := json.RawMessage(rootfsBytes)
		dualConfig["rootfs"] = &rootfsRaw

		// Update history
		historyBytes, err := json.Marshal(config.History)
		if err != nil {
			return nil, errors.Wrap(err, "marshal history")
		}
		historyRaw := json.RawMessage(historyBytes)
		dualConfig["history"] = &historyRaw

		return writeJSON(ctx, cs, &dualConfig, configDesc, labels)
	}

	// nolint:nilnil
	return nil, nil
}

// LayerReconvertFunc implements the platform-specific layer reconversion logic.
func LayerReconvertFunc(opt UnpackOption) containerdConverter.ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			// nolint:nilnil
			return nil, nil
		}

		// Skip the nydus bootstrap layer - don't convert it, and it will be removed by hook
		if IsNydusBootstrap(desc) {
			logrus.Debugf("skip nydus bootstrap layer %s (will be removed by hook)", desc.Digest.String())
			return &desc, nil
		}

		// Only convert Nydus blob layers, skip non-Nydus layers
		if !IsNydusBlob(desc) {
			logrus.Debugf("skip non-nydus blob layer %s", desc.Digest.String())
			// nolint:nilnil
			return nil, nil
		}

		ra, err := cs.ReaderAt(ctx, desc)
		if err != nil {
			return nil, errors.Wrap(err, "get reader")
		}
		defer ra.Close()

		ref := fmt.Sprintf("convert-oci-from-%s", desc.Digest)
		cw, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
		if err != nil {
			return nil, errors.Wrap(err, "open blob writer")
		}
		defer cw.Close()

		var gw io.WriteCloser
		var mediaType string
		compressor := opt.Compressor
		if compressor == "" {
			compressor = "gzip"
		}
		switch compressor {
		case "gzip":
			gw = gzip.NewWriter(cw)
			mediaType = ocispec.MediaTypeImageLayerGzip
		case "zstd":
			gw, err = zstd.NewWriter(cw)
			if err != nil {
				return nil, errors.Wrap(err, "create zstd writer")
			}
			mediaType = ocispec.MediaTypeImageLayerZstd
		case "uncompressed":
			gw = cw
			mediaType = ocispec.MediaTypeImageLayer
		default:
			return nil, errors.Errorf("unsupported compressor type: %s (support: gzip, zstd, uncompressed)", opt.Compressor)
		}

		uncompressedDgster := digest.SHA256.Digester()
		pr, pw := io.Pipe()

		// Unpack nydus blob to pipe writer in background
		go func() {
			defer pw.Close()
			if err := Unpack(ctx, ra, pw, opt); err != nil {
				pw.CloseWithError(errors.Wrap(err, "unpack nydus to tar"))
			}
		}()

		// Stream data from pipe reader to compressed writer and digester
		compressed := io.MultiWriter(gw, uncompressedDgster.Hash())
		buffer := bufPool.Get().(*[]byte)
		defer bufPool.Put(buffer)
		if _, err = io.CopyBuffer(compressed, pr, *buffer); err != nil {
			return nil, errors.Wrapf(err, "copy to compressed writer")
		}

		// Close compressor writer if different from content writer
		if gw != cw {
			if err = gw.Close(); err != nil {
				return nil, errors.Wrap(err, "close compressor writer")
			}
		}

		uncompressedDigest := uncompressedDgster.Digest()
		compressedDgst := cw.Digest()
		if err = cw.Commit(ctx, 0, compressedDgst, content.WithLabels(map[string]string{
			LayerAnnotationUncompressed: uncompressedDigest.String(),
		})); err != nil {
			if !errdefs.IsAlreadyExists(err) {
				return nil, errors.Wrap(err, "commit to content store")
			}
		}
		if err = cw.Close(); err != nil {
			return nil, errors.Wrap(err, "close content store writer")
		}

		newDesc, err := makeOCIBlobDesc(ctx, cs, uncompressedDigest, compressedDgst, mediaType)
		if err != nil {
			return nil, err
		}

		if opt.Backend != nil {
			if err := opt.Backend.Push(ctx, cs, *newDesc); err != nil {
				return nil, errors.Wrap(err, "push to storage backend")
			}
		}
		return newDesc, nil
	}
}
