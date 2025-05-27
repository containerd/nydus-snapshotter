/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package containerdreconverter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	// annotationSourceDigest indicates the source OCI image digest.
	annotationSourceDigest = "containerd.io/snapshot/nydus-source-digest"
	// annotationSourceReference indicates the source OCI image reference name.
	annotationSourceReference = "containerd.io/snapshot/nydus-source-reference"
	// annotationFsVersion indicates the fs version (rafs v5/v6) of nydus image.
	annotationFsVersion = "containerd.io/snapshot/nydus-fs-version"
	// annotationBuilderVersion indicates the nydus builder (nydus-image) version.
	annotationBuilderVersion = "containerd.io/snapshot/nydus-builder-version"
)

// ConvertFunc returns a converted content descriptor.
// When the content was not converted, ConvertFunc returns nil.
type ConvertFunc func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error)

// DefaultIndexConvertFunc is the default convert func used by Convert.
func DefaultIndexConvertFunc(layerConvertFunc ConvertFunc, docker2oci bool, platformMC platforms.MatchComparer) ConvertFunc {
	c := &defaultConverter{
		layerConvertFunc: layerConvertFunc,
		docker2oci:       docker2oci,
		platformMC:       platformMC,
		ocilayerMap:      make(map[string]bool),
		diffIDMap:        make(map[digest.Digest]digest.Digest),
	}
	return c.convert
}

// ConvertHookFunc is a callback function called during conversion of a blob.
// orgDesc is the target descriptor to convert. newDesc is passed if conversion happens.
type ConvertHookFunc func(ctx context.Context, cs content.Store, orgDesc ocispec.Descriptor, newDesc *ocispec.Descriptor) (*ocispec.Descriptor, error)

// ConvertHooks is a configuration for hook callbacks called during blob conversion.
type ConvertHooks struct {
	// PostConvertHook is a callback function called for each blob after conversion is done.
	PostConvertHook ConvertHookFunc
}

// IndexConvertFuncWithHook is the convert func used by Convert with hook functions support.
func IndexConvertFuncWithHook(layerConvertFunc ConvertFunc, docker2oci bool, platformMC platforms.MatchComparer, hooks ConvertHooks) ConvertFunc {
	c := &defaultConverter{
		layerConvertFunc: layerConvertFunc,
		docker2oci:       docker2oci,
		platformMC:       platformMC,
		diffIDMap:        make(map[digest.Digest]digest.Digest),
		ocilayerMap:      make(map[string]bool),
		hooks:            hooks,
	}
	return c.convert
}

type defaultConverter struct {
	layerConvertFunc ConvertFunc
	docker2oci       bool
	platformMC       platforms.MatchComparer
	diffIDMap        map[digest.Digest]digest.Digest // key: old diffID, value: new diffID
	ocilayerMap      map[string]bool                 // key: oci layer digest, value: true
	diffIDMapMu      sync.RWMutex
	hooks            ConvertHooks
}

// convert dispatches desc.MediaType and calls c.convert{Layer,Manifest,Index,Config}.
//
// Also converts media type if c.docker2oci is set.
func (c *defaultConverter) convert(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var (
		newDesc *ocispec.Descriptor
		err     error
	)

	if images.IsLayerType(desc.MediaType) {
		logrus.Debugf("case convert layer %s", desc.Digest.String())
		newDesc, err = c.convertLayer(ctx, cs, desc)
	} else if images.IsManifestType(desc.MediaType) {
		logrus.Debugf("case convert manifest %s", desc.Digest.String())
		newDesc, err = c.convertManifest(ctx, cs, desc)
	} else if images.IsIndexType(desc.MediaType) {
		logrus.Debugf("case convert index %s", desc.Digest.String())
		newDesc, err = c.convertIndex(ctx, cs, desc)
	} else if images.IsConfigType(desc.MediaType) {
		logrus.Debugf("case convert config %s", desc.Digest.String())
		newDesc, err = c.convertConfig(ctx, cs, desc)
	}
	if err != nil {
		return nil, err
	}

	if c.hooks.PostConvertHook != nil {
		if newDescPost, err := c.hooks.PostConvertHook(ctx, cs, desc, newDesc); err != nil {
			return nil, err
		} else if newDescPost != nil {
			newDesc = newDescPost
		}
	}

	if images.IsDockerType(desc.MediaType) {
		if c.docker2oci {
			if newDesc == nil {
				newDesc = copyDesc(desc)
			}
			newDesc.MediaType = ConvertDockerMediaTypeToOCI(newDesc.MediaType)
		} else if (newDesc == nil && len(desc.Annotations) != 0) || (newDesc != nil && len(newDesc.Annotations) != 0) {
			// Annotations is supported only on OCI manifest.
			// We need to remove annotations for Docker media types.
			if newDesc == nil {
				newDesc = copyDesc(desc)
			}
			// newDesc.Annotations = nil
		}
	}
	logrus.WithField("old", desc).WithField("new", newDesc).Debugf("converted")
	return newDesc, nil
}

func copyDesc(desc ocispec.Descriptor) *ocispec.Descriptor {
	descCopy := desc
	return &descCopy
}

// convertLayer converts image layers if c.layerConvertFunc is set.
//
// c.layerConvertFunc can be nil, e.g., for converting Docker media types to OCI ones.
func (c *defaultConverter) convertLayer(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	if c.layerConvertFunc != nil {
		return c.layerConvertFunc(ctx, cs, desc)
	}
	return nil, nil
}

// convertManifest converts image manifests.
//
// - converts `.mediaType` if the target format is OCI
// - records diff ID changes in c.diffIDMap
func (c *defaultConverter) convertManifest(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var (
		manifest ocispec.Manifest
		modified bool
	)
	labels, err := readJSON(ctx, cs, &manifest, desc)
	if err != nil {
		return nil, err
	}

	if labels == nil {
		labels = make(map[string]string)
	}
	if images.IsDockerType(manifest.MediaType) && c.docker2oci {
		manifest.MediaType = ConvertDockerMediaTypeToOCI(manifest.MediaType)
		modified = true
	}
	var mu sync.Mutex
	eg, ctx2 := errgroup.WithContext(ctx)
	for i, l := range manifest.Layers {
		i := i
		l := l
		// get digest id
		oldDiffID := l.Digest
		eg.Go(func() error {
			newL, err := c.convert(ctx2, cs, l)
			if err != nil {
				return err
			}
			if newL != nil && IsNydusBootstrap(*newL) {
				// delete nydus bootstrap layer
				mu.Lock()
				ClearGCLabels(labels, newL.Digest)
				manifest.Layers = slices.Delete(manifest.Layers, i, i+1)
				modified = true
				mu.Unlock()
			} else if newL != nil {
				mu.Lock()
				// update GC labels
				ClearGCLabels(labels, l.Digest)
				labelKey := fmt.Sprintf("containerd.io/gc.ref.content.l.%d", i)
				labels[labelKey] = newL.Digest.String()
				manifest.Layers[i] = *newL
				modified = true
				mu.Unlock()

				// diffID changes if the tar entries were modified.
				// diffID stays same if only the compression type was changed.
				// When diffID changed, add a map entry so that we can update image config.
				newDiffID := newL.Digest
				if uncompress, ok := newL.Annotations[LayerAnnotationUncompressed]; ok {
					newDiffID = digest.Digest(uncompress)
				}
				if newDiffID != oldDiffID {
					c.diffIDMapMu.Lock()
					c.diffIDMap[oldDiffID] = newDiffID
					c.diffIDMapMu.Unlock()
				}
				c.ocilayerMap[newL.Digest.String()] = true
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	newConfig, err := c.convert(ctx, cs, manifest.Config)
	if err != nil {
		return nil, err
	}
	if newConfig != nil {
		ClearGCLabels(labels, manifest.Config.Digest)
		labels["containerd.io/gc.ref.content.config"] = newConfig.Digest.String()
		manifest.Config = *newConfig
		modified = true
	}

	if modified {
		return writeJSON(ctx, cs, &manifest, desc, labels)
	}
	return nil, nil
}

// convertIndex converts image index.
//
// - converts `.mediaType` if the target format is OCI
// - clears manifest entries that do not match c.platformMC
func (c *defaultConverter) convertIndex(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var (
		index    ocispec.Index
		modified bool
	)
	labels, err := readJSON(ctx, cs, &index, desc)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if images.IsDockerType(index.MediaType) && c.docker2oci {
		index.MediaType = ConvertDockerMediaTypeToOCI(index.MediaType)
		modified = true
	}

	newManifests := make([]ocispec.Descriptor, len(index.Manifests))
	newManifestsToBeRemoved := make(map[int]struct{}) // slice index
	var mu sync.Mutex
	eg, ctx2 := errgroup.WithContext(ctx)
	for i, mani := range index.Manifests {
		i := i
		mani := mani
		labelKey := fmt.Sprintf("containerd.io/gc.ref.content.m.%d", i)
		eg.Go(func() error {
			if mani.Platform != nil && !c.platformMC.Match(*mani.Platform) {
				mu.Lock()
				ClearGCLabels(labels, mani.Digest)
				newManifestsToBeRemoved[i] = struct{}{}
				modified = true
				mu.Unlock()
				return nil
			}
			newMani, err := c.convert(ctx2, cs, mani)
			if err != nil {
				return err
			}
			mu.Lock()
			if newMani != nil {
				ClearGCLabels(labels, mani.Digest)
				labels[labelKey] = newMani.Digest.String()
				// NOTE: for keeping manifest order, we specify `i` index explicitly
				newManifests[i] = *newMani
				modified = true
			} else {
				newManifests[i] = mani
			}
			mu.Unlock()
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	if modified {
		var newManifestsClean []ocispec.Descriptor
		for i, m := range newManifests {
			if _, ok := newManifestsToBeRemoved[i]; !ok {
				newManifestsClean = append(newManifestsClean, m)
			}
		}
		index.Manifests = newManifestsClean
		return writeJSON(ctx, cs, &index, desc, labels)
	}
	return nil, nil
}

// convertConfig converts image config contents.
//
// - updates `.rootfs.diff_ids` using c.diffIDMap .
//
// - clears legacy `.config.Image` and `.container_config.Image` fields if `.rootfs.diff_ids` was updated.
func (c *defaultConverter) convertConfig(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
	var (
		cfg      DualConfig
		cfgAsOCI ocispec.Image // read only, used for parsing cfg
		modified bool
	)

	labels, err := readJSON(ctx, cs, &cfg, desc)
	if err != nil {
		return nil, err
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if _, err := readJSON(ctx, cs, &cfgAsOCI, desc); err != nil {
		return nil, err
	}

	if rootfs := cfgAsOCI.RootFS; rootfs.Type == "layers" {
		rootfsModified := false
		c.diffIDMapMu.RLock()
		for i, oldDiffID := range rootfs.DiffIDs {
			if newDiffID, ok := c.diffIDMap[oldDiffID]; ok && newDiffID != oldDiffID {
				rootfs.DiffIDs[i] = newDiffID
				rootfsModified = true
			} else if _, ok := c.ocilayerMap[oldDiffID.String()]; !ok {
				logrus.Debugf("remove diffid: %s", oldDiffID.String())
				rootfs.DiffIDs = slices.Delete(rootfs.DiffIDs, i, i+1)
			}
		}
		c.diffIDMapMu.RUnlock()
		if rootfsModified {
			rootfsB, err := json.Marshal(rootfs)
			if err != nil {
				return nil, err
			}
			cfg["rootfs"] = (*json.RawMessage)(&rootfsB)
			modified = true
		}
	}

	for i, h := range cfgAsOCI.History {
		if h.Comment == "Nydus Bootstrap Layer" && h.CreatedBy == "Nydus Converter" {
			// Remove the history entry of nydus bootstrap layer.
			// We don't need to convert nydus bootstrap layer.
			cfgAsOCI.History = slices.Delete(cfgAsOCI.History, i, i+1)
			modified = true
			break
		}
	}

	for i, h := range cfgAsOCI.History {
		if h.EmptyLayer {
			// Remove the history entry of empty layer.
			cfgAsOCI.History = slices.Delete(cfgAsOCI.History, i, i+1)
			modified = true
			continue
		}
	}

	// 处理too many non-empty layers in History section
	if len(cfgAsOCI.RootFS.DiffIDs) < len(cfgAsOCI.History) {
		cfgAsOCI.History = []ocispec.History{}
	}

	if modified {
		// 处理history变化
		historyJson, err := json.Marshal(cfgAsOCI.History)
		if err != nil {
			return nil, err
		}
		cfg["history"] = (*json.RawMessage)(&historyJson)

		// cfg may have dummy value for legacy `.config.Image` and `.container_config.Image`
		// We should clear the ID if we changed the diff IDs.
		if _, err := clearDockerV1DummyID(cfg); err != nil {
			return nil, err
		}
		return writeJSON(ctx, cs, &cfg, desc, labels)
	}
	return nil, nil
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

func readJSON(ctx context.Context, cs content.Store, x interface{}, desc ocispec.Descriptor) (map[string]string, error) {
	info, err := cs.Info(ctx, desc.Digest)
	if err != nil {
		return nil, err
	}
	labels := info.Labels
	b, err := content.ReadBlob(ctx, cs, desc)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, x); err != nil {
		return nil, err
	}
	return labels, nil
}

func writeJSON(ctx context.Context, cs content.Store, x interface{}, oldDesc ocispec.Descriptor, labels map[string]string) (*ocispec.Descriptor, error) {
	b, err := json.Marshal(x)
	if err != nil {
		return nil, err
	}
	dgst := digest.SHA256.FromBytes(b)
	ref := fmt.Sprintf("converter-write-json-%s", dgst.String())
	w, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
	if err != nil {
		return nil, err
	}
	if err := content.Copy(ctx, w, bytes.NewReader(b), int64(len(b)), dgst, content.WithLabels(labels)); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	newDesc := oldDesc
	newDesc.Size = int64(len(b))
	newDesc.Digest = dgst
	return &newDesc, nil
}

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
		return ocispec.MediaTypeImageLayerNonDistributableGzip
	case images.MediaTypeDockerSchema2Layer:
		return ocispec.MediaTypeImageLayer
	case images.MediaTypeDockerSchema2LayerForeign:
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

func IsNydusLayerType(mt string) bool {
	if strings.HasPrefix(mt, "application/vnd.oci.image.layer.nydus") {
		return true
	}
	return false
}

func RemoveNydusAnnos(annos map[string]string) map[string]string {
	for k := range annos {
		if k == annotationFsVersion {
			delete(annos, k)
			continue
		}
		if k == annotationSourceReference {
			delete(annos, k)
			continue
		}
		if k == annotationSourceDigest {
			delete(annos, k)
			continue
		}
		if k == annotationBuilderVersion {
			delete(annos, k)
			continue
		}
	}
	return annos
}

func Annotate(annotations map[string]string, appended map[string]string) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	for k, v := range appended {
		annotations[k] = v
	}

	return annotations
}
