//go:build !windows
// +build !windows

/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockContentStore implements content.Store interface for testing
type mockContentStore struct {
	content.Store
	infos map[digest.Digest]content.Info
	blobs map[digest.Digest][]byte
}

func newMockContentStore() *mockContentStore {
	return &mockContentStore{
		infos: make(map[digest.Digest]content.Info),
		blobs: make(map[digest.Digest][]byte),
	}
}

func (m *mockContentStore) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	if info, ok := m.infos[dgst]; ok {
		return info, nil
	}
	return content.Info{}, ErrNotFound
}

func (m *mockContentStore) addBlob(dgst digest.Digest, data []byte, labels map[string]string) {
	m.infos[dgst] = content.Info{
		Digest: dgst,
		Size:   int64(len(data)),
		Labels: labels,
	}
	m.blobs[dgst] = data
}

func (m *mockContentStore) ReaderAt(_ context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	if data, ok := m.blobs[desc.Digest]; ok {
		return &mockReaderAt{data: data}, nil
	}
	return nil, ErrNotFound
}

type mockReaderAt struct {
	data []byte
}

func (r *mockReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= int64(len(r.data)) {
		return 0, ErrNotFound
	}
	n = copy(p, r.data[off:])
	return n, nil
}

func (r *mockReaderAt) Size() int64 {
	return int64(len(r.data))
}

func (r *mockReaderAt) Close() error {
	return nil
}

func TestCollectNydusBlobDigests(t *testing.T) {
	ctx := context.Background()

	// Create test digests
	nydusBlob1 := digest.FromString("nydus-blob-1")
	nydusBlob2 := digest.FromString("nydus-blob-2")
	regularBlob := digest.FromString("regular-blob")
	manifestDigest := digest.FromString("manifest")
	indexDigest := digest.FromString("index")

	// Test cases
	tests := []struct {
		name          string
		setupStore    func(*mockContentStore)
		desc          ocispec.Descriptor
		expectedBlobs map[digest.Digest]bool
		expectedCount int
	}{
		{
			name: "manifest with nydus blobs",
			setupStore: func(cs *mockContentStore) {
				manifest := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: MediaTypeNydusBlob,
							Digest:    nydusBlob1,
							Annotations: map[string]string{
								LayerAnnotationNydusBlob: "true",
							},
						},
						{
							MediaType: ocispec.MediaTypeImageLayerGzip,
							Digest:    regularBlob,
						},
					},
				}
				data, _ := json.Marshal(manifest)
				cs.addBlob(manifestDigest, data, nil)
			},
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    manifestDigest,
			},
			expectedCount: 1,
		},
		{
			name: "index with multiple manifests",
			setupStore: func(cs *mockContentStore) {
				// Create two manifests
				manifest1 := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: MediaTypeNydusBlob,
							Digest:    nydusBlob1,
							Annotations: map[string]string{
								LayerAnnotationNydusBlob: "true",
							},
						},
					},
				}
				manifest1Data, _ := json.Marshal(manifest1)
				manifest1Digest := digest.FromBytes(manifest1Data)
				cs.addBlob(manifest1Digest, manifest1Data, nil)

				manifest2 := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: MediaTypeNydusBlob,
							Digest:    nydusBlob2,
							Annotations: map[string]string{
								LayerAnnotationNydusBlob: "true",
							},
						},
					},
				}
				manifest2Data, _ := json.Marshal(manifest2)
				manifest2Digest := digest.FromBytes(manifest2Data)
				cs.addBlob(manifest2Digest, manifest2Data, nil)

				// Create index
				index := ocispec.Index{
					Manifests: []ocispec.Descriptor{
						{
							MediaType: ocispec.MediaTypeImageManifest,
							Digest:    manifest1Digest,
						},
						{
							MediaType: ocispec.MediaTypeImageManifest,
							Digest:    manifest2Digest,
						},
					},
				}
				indexData, _ := json.Marshal(index)
				cs.addBlob(indexDigest, indexData, nil)
			},
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageIndex,
				Digest:    indexDigest,
			},
			expectedCount: 2,
		},
		{
			name: "manifest with no nydus blobs",
			setupStore: func(cs *mockContentStore) {
				manifest := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: ocispec.MediaTypeImageLayerGzip,
							Digest:    regularBlob,
						},
					},
				}
				data, _ := json.Marshal(manifest)
				cs.addBlob(manifestDigest, data, nil)
			},
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    manifestDigest,
			},
			expectedCount: 0,
		},
		{
			name: "invalid descriptor type",
			setupStore: func(_ *mockContentStore) {
				// No setup needed
			},
			desc: ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageConfig,
				Digest:    digest.FromString("config"),
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := newMockContentStore()
			tt.setupStore(cs)

			blobs := collectNydusBlobDigests(ctx, cs, tt.desc)
			assert.Equal(t, tt.expectedCount, len(blobs))
		})
	}
}

func TestCollectFromManifest(t *testing.T) {
	ctx := context.Background()

	nydusBlob1 := digest.FromString("nydus-blob-1")
	nydusBlob2 := digest.FromString("nydus-blob-2")
	bootstrapBlob := digest.FromString("bootstrap-blob")
	regularBlob := digest.FromString("regular-blob")
	manifestDigest := digest.FromString("manifest")

	tests := []struct {
		name          string
		setupManifest func() (ocispec.Manifest, []byte)
		expectedCount int
		expectedBlobs []digest.Digest
	}{
		{
			name: "manifest with multiple nydus blobs and bootstrap",
			setupManifest: func() (ocispec.Manifest, []byte) {
				manifest := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: MediaTypeNydusBlob,
							Digest:    nydusBlob1,
							Annotations: map[string]string{
								LayerAnnotationNydusBlob: "true",
							},
						},
						{
							MediaType: MediaTypeNydusBlob,
							Digest:    nydusBlob2,
							Annotations: map[string]string{
								LayerAnnotationNydusBlob: "true",
							},
						},
						{
							MediaType: ocispec.MediaTypeImageLayer,
							Digest:    bootstrapBlob,
							Annotations: map[string]string{
								LayerAnnotationNydusBootstrap: "true",
							},
						},
						{
							MediaType: ocispec.MediaTypeImageLayerGzip,
							Digest:    regularBlob,
						},
					},
				}
				data, _ := json.Marshal(manifest)
				return manifest, data
			},
			expectedCount: 2,
			expectedBlobs: []digest.Digest{nydusBlob1, nydusBlob2},
		},
		{
			name: "manifest with only regular layers",
			setupManifest: func() (ocispec.Manifest, []byte) {
				manifest := ocispec.Manifest{
					Layers: []ocispec.Descriptor{
						{
							MediaType: ocispec.MediaTypeImageLayerGzip,
							Digest:    regularBlob,
						},
					},
				}
				data, _ := json.Marshal(manifest)
				return manifest, data
			},
			expectedCount: 0,
			expectedBlobs: []digest.Digest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := newMockContentStore()
			_, manifestData := tt.setupManifest()
			cs.addBlob(manifestDigest, manifestData, nil)

			nydusBlobs := make(map[digest.Digest]bool)
			desc := ocispec.Descriptor{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    manifestDigest,
			}

			collectFromManifest(ctx, cs, desc, nydusBlobs)

			assert.Equal(t, tt.expectedCount, len(nydusBlobs))
			for _, expectedBlob := range tt.expectedBlobs {
				assert.True(t, nydusBlobs[expectedBlob], "expected blob %s not found", expectedBlob)
			}
		})
	}
}

func TestWrappedStore_Info(t *testing.T) {
	ctx := context.Background()

	nydusBlob := digest.FromString("nydus-blob")
	regularBlob := digest.FromString("regular-blob")

	tests := []struct {
		name             string
		digest           digest.Digest
		isNydusBlob      bool
		existingLabels   map[string]string
		shouldAddLabel   bool
		expectedLabelVal string
	}{
		{
			name:             "nydus blob without existing labels",
			digest:           nydusBlob,
			isNydusBlob:      true,
			existingLabels:   nil,
			shouldAddLabel:   true,
			expectedLabelVal: nydusBlob.String(),
		},
		{
			name:        "nydus blob with existing labels",
			digest:      nydusBlob,
			isNydusBlob: true,
			existingLabels: map[string]string{
				"other.label": "value",
			},
			shouldAddLabel:   true,
			expectedLabelVal: nydusBlob.String(),
		},
		{
			name:        "regular blob",
			digest:      regularBlob,
			isNydusBlob: false,
			existingLabels: map[string]string{
				"other.label": "value",
			},
			shouldAddLabel:   false,
			expectedLabelVal: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := newMockContentStore()
			cs.addBlob(tt.digest, []byte("test data"), tt.existingLabels)

			nydusBlobs := make(map[digest.Digest]bool)
			if tt.isNydusBlob {
				nydusBlobs[tt.digest] = true
			}

			ws := &wrappedStore{
				Store:      cs,
				nydusBlobs: nydusBlobs,
			}

			info, err := ws.Info(ctx, tt.digest)
			require.NoError(t, err)
			assert.Equal(t, tt.digest, info.Digest)

			if tt.shouldAddLabel {
				assert.NotNil(t, info.Labels)
				assert.Equal(t, tt.expectedLabelVal, info.Labels["containerd.io/uncompressed"])
			} else if info.Labels != nil {
				assert.Empty(t, info.Labels["containerd.io/uncompressed"])
			}
		})
	}
}

func TestWrappedStore_Info_Error(t *testing.T) {
	ctx := context.Background()
	cs := newMockContentStore()
	ws := &wrappedStore{
		Store:      cs,
		nydusBlobs: make(map[digest.Digest]bool),
	}

	nonExistentDigest := digest.FromString("non-existent")
	_, err := ws.Info(ctx, nonExistentDigest)
	assert.Error(t, err)
	assert.Equal(t, ErrNotFound, err)
}

func TestMakeOCIBlobDesc(t *testing.T) {
	ctx := context.Background()

	uncompressedDigest := digest.FromString("uncompressed-data")
	targetDigest := digest.FromString("compressed-data")

	tests := []struct {
		name              string
		uncompressedDgst  digest.Digest
		targetDgst        digest.Digest
		mediaType         string
		existingLabels    map[string]string
		expectedMediaType string
		shouldFail        bool
	}{
		{
			name:              "gzip layer",
			uncompressedDgst:  uncompressedDigest,
			targetDgst:        targetDigest,
			mediaType:         ocispec.MediaTypeImageLayerGzip,
			existingLabels:    nil,
			expectedMediaType: ocispec.MediaTypeImageLayerGzip,
			shouldFail:        false,
		},
		{
			name:              "zstd layer",
			uncompressedDgst:  uncompressedDigest,
			targetDgst:        targetDigest,
			mediaType:         ocispec.MediaTypeImageLayerZstd,
			existingLabels:    nil,
			expectedMediaType: ocispec.MediaTypeImageLayerZstd,
			shouldFail:        false,
		},
		{
			name:              "uncompressed layer",
			uncompressedDgst:  uncompressedDigest,
			targetDgst:        targetDigest,
			mediaType:         ocispec.MediaTypeImageLayer,
			existingLabels:    nil,
			expectedMediaType: ocispec.MediaTypeImageLayer,
			shouldFail:        false,
		},
		{
			name:             "non-existent target",
			uncompressedDgst: uncompressedDigest,
			targetDgst:       digest.FromString("non-existent"),
			mediaType:        ocispec.MediaTypeImageLayerGzip,
			shouldFail:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := newMockContentStore()

			if !tt.shouldFail {
				cs.addBlob(tt.targetDgst, []byte("compressed data"), tt.existingLabels)
			}

			desc, err := makeOCIBlobDesc(ctx, cs, tt.uncompressedDgst, tt.targetDgst, tt.mediaType)

			if tt.shouldFail {
				assert.Error(t, err)
				assert.Nil(t, desc)
			} else {
				require.NoError(t, err)
				require.NotNil(t, desc)
				assert.Equal(t, tt.targetDgst, desc.Digest)
				assert.Equal(t, tt.expectedMediaType, desc.MediaType)
				assert.Equal(t, int64(len("compressed data")), desc.Size)
				assert.NotNil(t, desc.Annotations)
				assert.Equal(t, tt.uncompressedDgst.String(), desc.Annotations[LayerAnnotationUncompressed])
			}
		})
	}
}

func TestReconvertHookFunc_NilDescriptor(t *testing.T) {
	ctx := context.Background()
	cs := newMockContentStore()

	hook := ReconvertHookFunc()
	result, err := hook(ctx, cs, ocispec.Descriptor{}, nil)

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestReconvertHookFunc_NonManifestType(t *testing.T) {
	ctx := context.Background()
	cs := newMockContentStore()

	desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromString("config"),
	}

	hook := ReconvertHookFunc()
	result, err := hook(ctx, cs, ocispec.Descriptor{}, &desc)

	assert.NoError(t, err)
	assert.Equal(t, &desc, result)
}
