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
	"strings"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/klauspost/compress/zstd"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// ConvertFunc returns a converted content descriptor.
// When the content was not converted, ConvertFunc returns nil.
type ConvertFunc = converter.ConvertFunc

// DefaultIndexConvertFunc wraps the upstream default converter and adds Nydus-specific post processing.
func DefaultIndexConvertFunc(layerConvertFunc ConvertFunc, docker2oci bool, platformMC platforms.MatchComparer) ConvertFunc {
	upstream := converter.DefaultIndexConvertFunc(layerConvertFunc, docker2oci, platformMC)
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		dst, err := upstream(ctx, cs, desc)
		if err != nil {
			return nil, err
		}
		if dst == nil {
			return nil, nil
		}

		if images.IsManifestType(dst.MediaType) {
			var manifest ocispec.Manifest
			labels, err := readJSON(ctx, cs, &manifest, *dst)
			if err != nil {
				return nil, err
			}
			if labels == nil {
				labels = make(map[string]string)
			}

			// Remove nydus bootstrap layer if present
			newLayers := make([]ocispec.Descriptor, 0, len(manifest.Layers))
			for _, l := range manifest.Layers {
				if IsNydusBootstrap(l) {
					ClearGCLabels(labels, l.Digest)
					continue
				}
				newLayers = append(newLayers, l)
			}
			manifest.Layers = newLayers

			// Update config: rebuild diffIDs from layer annotations and clean history
			var cfg DualConfig
			var cfgOCI ocispec.Image
			cfgLabels, err := readJSON(ctx, cs, &cfg, manifest.Config)
			if err != nil {
				return nil, err
			}
			if _, err := readJSON(ctx, cs, &cfgOCI, manifest.Config); err != nil {
				return nil, err
			}

			var diffIDs []digest.Digest
			for _, l := range manifest.Layers {
				if uncompress, ok := l.Annotations[LayerAnnotationUncompressed]; ok {
					diffIDs = append(diffIDs, digest.Digest(uncompress))
				} else {
					diffIDs = append(diffIDs, l.Digest)
				}
			}
			cfgOCI.RootFS.Type = "layers"
			cfgOCI.RootFS.DiffIDs = diffIDs

			// Remove bootstrap and empty layers from history
			for i, h := range cfgOCI.History {
				if h.Comment == "Nydus Bootstrap Layer" && h.CreatedBy == "Nydus Converter" {
					cfgOCI.History = slices.Delete(cfgOCI.History, i, i+1)
					break
				}
			}
			for i, h := range cfgOCI.History {
				if h.EmptyLayer {
					cfgOCI.History = slices.Delete(cfgOCI.History, i, i+1)
				}
			}
			if len(cfgOCI.RootFS.DiffIDs) < len(cfgOCI.History) {
				cfgOCI.History = []ocispec.History{}
			}

			historyJSON, err := json.Marshal(cfgOCI.History)
			if err != nil {
				return nil, err
			}
			cfg["history"] = (*json.RawMessage)(&historyJSON)

			rootfsJSON, err := json.Marshal(cfgOCI.RootFS)
			if err != nil {
				return nil, err
			}
			cfg["rootfs"] = (*json.RawMessage)(&rootfsJSON)

			if _, err := clearDockerV1DummyID(cfg); err != nil {
				return nil, err
			}
			newConfig, err := writeJSON(ctx, cs, &cfg, manifest.Config, cfgLabels)
			if err != nil {
				return nil, err
			}

			ClearGCLabels(labels, manifest.Config.Digest)
			labels["containerd.io/gc.ref.content.config"] = newConfig.Digest.String()
			manifest.Config = *newConfig

			return writeJSON(ctx, cs, &manifest, *dst, labels)
		}

		// For index or other types, return upstream result
		return dst, nil
	}
}

// clearDockerV1DummyID clears the dummy values for legacy `.config.Image` and `.container_config.Image`.
// Returns true if the cfg was modified.
func clearDockerV1DummyID(cfg DualConfig) (bool, error) {
	var modified bool
	f := func(k string) error {
		if configX, ok := cfg[k]; ok && configX != nil {
			var configField map[string]*json.RawMessage
			if err := json.Unmarshal(*configX, &configField); err != nil {
				return err
			}
			delete(configField, "Image")
			b, err := json.Marshal(configField)
			if err != nil {
				return err
			}
			cfg[k] = (*json.RawMessage)(&b)
			modified = true
		}
		return nil
	}
	if err := f("config"); err != nil {
		return modified, err
	}
	if err := f("container_config"); err != nil {
		return modified, err
	}
	return modified, nil
}

// DualConfig covers Docker config (v1.0, v1.1, v1.2) and OCI config.
// Unmarshalled as map[string]*json.RawMessage to retain unknown fields on remarshalling.
type DualConfig map[string]*json.RawMessage

// ConvertDockerMediaTypeToOCI converts a media type string
func ConvertDockerMediaTypeToOCI(mt string) string {
	switch mt {
	case images.MediaTypeDockerSchema2ManifestList:
		return ocispec.MediaTypeImageIndex
	case images.MediaTypeDockerSchema2Manifest:
		return ocispec.MediaTypeImageManifest
	case images.MediaTypeDockerSchema2LayerGzip:
		return ocispec.MediaTypeImageLayerGzip
	case images.MediaTypeDockerSchema2LayerForeignGzip:
		//nolint:staticcheck // Converting from existing Docker format that may contain deprecated layer types
		return ocispec.MediaTypeImageLayerNonDistributableGzip
	case images.MediaTypeDockerSchema2Layer:
		return ocispec.MediaTypeImageLayer
	case images.MediaTypeDockerSchema2LayerForeign:
		//nolint:staticcheck // Converting from existing Docker format that may contain deprecated layer types
		return ocispec.MediaTypeImageLayerNonDistributable
	case images.MediaTypeDockerSchema2Config:
		return ocispec.MediaTypeImageConfig
	default:
		return mt
	}
}

// ClearGCLabels clears GC labels for the given digest.
func ClearGCLabels(labels map[string]string, dgst digest.Digest) {
	for k, v := range labels {
		if v == dgst.String() && strings.HasPrefix(k, "containerd.io/gc.ref.content") {
			delete(labels, k)
		}
	}
}

func makeOCIBlobDesc(ctx context.Context, cs content.Store, uncompressedDigest, targetDigest digest.Digest, mediaType string) (*ocispec.Descriptor, error) {
	targetInfo, err := cs.Info(ctx, targetDigest)
	if err != nil {
		return nil, errors.Wrapf(err, "get target blob info %s", targetDigest)
	}
	if targetInfo.Labels == nil {
		targetInfo.Labels = map[string]string{}
	}

	targetDesc := ocispec.Descriptor{
		Digest:    targetDigest,
		Size:      targetInfo.Size,
		MediaType: mediaType,
		Annotations: map[string]string{
			LayerAnnotationUncompressed: uncompressedDigest.String(),
		},
	}

	return &targetDesc, nil
}

func ReconvertHookFunc() converter.ConvertHookFunc {
	return func(ctx context.Context, cs content.Store, _ ocispec.Descriptor, newDesc *ocispec.Descriptor) (*ocispec.Descriptor, error) {
		desc := newDesc
		if !images.IsManifestType(desc.MediaType) {
			return desc, nil
		}
		if IsNydusBootstrap(*desc) {
			return desc, nil
		}
		var err error
		var labels map[string]string
		switch desc.MediaType {
		case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
			var manifest ocispec.Manifest
			labels, err = readJSON(ctx, cs, &manifest, *desc)
			if err != nil {
				return nil, errors.Wrap(err, "read manifest")
			}

			desc, err = writeJSON(ctx, cs, manifest, *desc, labels)
			if err != nil {
				return nil, errors.Wrap(err, "write manifest")
			}
			return desc, nil

		case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
			var index ocispec.Index
			labels, err = readJSON(ctx, cs, &index, *desc)
			if err != nil {
				return nil, errors.Wrap(err, "read manifest index")
			}
			for idx, maniDesc := range index.Manifests {
				var manifest ocispec.Manifest
				labels, err = readJSON(ctx, cs, &manifest, maniDesc)
				if err != nil {
					return nil, errors.Wrap(err, "read manifest")
				}

				newManiDesc, err := writeJSON(ctx, cs, manifest, maniDesc, labels)
				if err != nil {
					return nil, errors.Wrap(err, "write manifest")
				}
				index.Manifests[idx] = *newManiDesc
			}
			desc, err = writeJSON(ctx, cs, index, *desc, labels)
			if err != nil {
				return nil, errors.Wrap(err, "write manifest index")
			}

			return desc, nil

		default:
			return nil, errors.Errorf("unsupported media type %s", desc.MediaType)
		}
	}
}

func LayerReconvertFunc(opt UnpackOption) ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			return nil, nil
		}

		if IsNydusBootstrap(desc) {
			logrus.Debugf("skip nydus bootstrap layer %s", desc.Digest.String())
			return &desc, nil
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

		go func() {
			defer pw.Close()
			if err := Unpack(ctx, ra, pw, opt); err != nil {
				pw.CloseWithError(errors.Wrap(err, "unpack nydus to tar"))
			}
		}()

		compressed := io.MultiWriter(gw, uncompressedDgster.Hash())
		buffer := bufPool.Get().(*[]byte)
		defer bufPool.Put(buffer)
		if _, err = io.CopyBuffer(compressed, pr, *buffer); err != nil {
			return nil, errors.Wrapf(err, "copy to compressed writer")
		}

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
