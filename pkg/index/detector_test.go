/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package index

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasNydusFeatures(t *testing.T) {
	d := &detector{}

	tests := []struct {
		name     string
		platform *ocispec.Platform
		expected bool
	}{
		{
			name:     "nil platform",
			platform: nil,
			expected: false,
		},
		{
			name: "platform without os features",
			platform: &ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
			},
			expected: false,
		},
		{
			name: "platform with nydus features",
			platform: &ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
				OSFeatures:   []string{"nydus.remoteimage.v1"},
			},
			expected: true,
		},
		{
			name: "platform with multiple features including nydus",
			platform: &ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
				OSFeatures:   []string{"feature1", "nydus.remoteimage.v1", "feature2"},
			},
			expected: true,
		},
		{
			name: "platform with non-nydus features",
			platform: &ocispec.Platform{
				OS:           "linux",
				Architecture: "amd64",
				OSFeatures:   []string{"feature1", "feature2"},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.hasNydusFeatures(tt.platform)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindNydusManifestInIndex(t *testing.T) {
	d := &detector{}

	manifestDigest := digest.FromString("test-manifest")
	nydusManifestDigest := digest.FromString("nydus-manifest")

	tests := []struct {
		name           string
		index          ocispec.Index
		manifestDigest digest.Digest
		expectedDigest *digest.Digest
		expectError    bool
		errorContains  string
	}{
		{
			name: "original manifest not found in index",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{},
			},
			manifestDigest: manifestDigest,
			expectError:    true,
			errorContains:  "not found in index",
		},
		{
			name: "no nydus alternative found",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: digest.FromString("other-manifest"),
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
				},
			},
			manifestDigest: manifestDigest,
			expectError:    true,
			errorContains:  "no nydus alternative found",
		},
		{
			name: "nydus alternative found",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: nydusManifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
							OSFeatures:   []string{"nydus.remoteimage.v1"},
						},
					},
				},
			},
			manifestDigest: manifestDigest,
			expectedDigest: &nydusManifestDigest,
			expectError:    false,
		},
		{
			name: "multiple nydus alternatives, returns first match",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: nydusManifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
							OSFeatures:   []string{"nydus.remoteimage.v1"},
						},
					},
					{
						Digest: digest.FromString("second-nydus-manifest"),
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
							OSFeatures:   []string{"nydus.remoteimage.v1"},
						},
					},
				},
			},
			manifestDigest: manifestDigest,
			expectedDigest: &nydusManifestDigest,
			expectError:    false,
		},
		{
			name: "nydus alternative with different architecture ignored",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: digest.FromString("nydus-arm64"),
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "arm64",
							OSFeatures:   []string{"nydus.remoteimage.v1"},
						},
					},
				},
			},
			manifestDigest: manifestDigest,
			expectError:    true,
			errorContains:  "no nydus alternative found",
		},
		{
			name: "nydus alternative found with artifact type",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: nydusManifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
						ArtifactType: "application/vnd.nydus.image.manifest.v1+json",
					},
				},
			},
			manifestDigest: manifestDigest,
			expectedDigest: &nydusManifestDigest,
			expectError:    false,
		},
		{
			name: "different artifact type is ignored",
			index: ocispec.Index{
				Manifests: []ocispec.Descriptor{
					{
						Digest: manifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
					},
					{
						Digest: nydusManifestDigest,
						Platform: &ocispec.Platform{
							OS:           "linux",
							Architecture: "amd64",
						},
						ArtifactType: "application/foo+bar",
					},
				},
			},
			manifestDigest: manifestDigest,
			expectError:    true,
			errorContains:  "no nydus alternative found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := d.findNydusManifestInIndex(tt.index, tt.manifestDigest)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, *tt.expectedDigest, result.Digest)
			}
		})
	}
}

func TestCheckIndexAlternativeUsesCertsDMirror(t *testing.T) {
	originalDigest := digest.FromString("original manifest")
	nydusDigest := digest.FromString("nydus manifest")
	metaLayerDigest := digest.FromString("nydus bootstrap")

	indexBytes, err := json.Marshal(ocispec.Index{
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    originalDigest,
				Size:      100,
				Platform:  &ocispec.Platform{OS: "linux", Architecture: "amd64"},
			},
			{
				MediaType: ocispec.MediaTypeImageManifest,
				Digest:    nydusDigest,
				Size:      100,
				Platform: &ocispec.Platform{
					OS:           "linux",
					Architecture: "amd64",
					OSFeatures:   []string{converter.ManifestOSFeatureNydus},
				},
			},
		},
	})
	assert.NoError(t, err)
	indexDigest := digest.FromBytes(indexBytes)

	manifestBytes, err := json.Marshal(ocispec.Manifest{
		MediaType: ocispec.MediaTypeImageManifest,
		Layers: []ocispec.Descriptor{
			{
				MediaType:   ocispec.MediaTypeImageLayerGzip,
				Digest:      metaLayerDigest,
				Size:        42,
				Annotations: map[string]string{label.NydusMetaLayer: "true"},
			},
		},
	})
	assert.NoError(t, err)

	const dragonflyRegistry = "https://registry-1.docker.io"
	var (
		mu       sync.Mutex
		requests []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()

		assert.Equal(t, dragonflyRegistry, r.Header.Get("X-Dragonfly-Registry"))

		switch r.URL.Path {
		case "/v2/library/nginx/manifests/latest":
			w.Header().Set("Content-Type", string(ocispec.MediaTypeImageIndex))
			w.Header().Set("Docker-Content-Digest", indexDigest.String())
			w.Header().Set("Content-Length", strconv.Itoa(len(indexBytes)))
			if r.Method == http.MethodGet {
				_, _ = w.Write(indexBytes)
			}
		case "/v2/library/nginx/manifests/" + indexDigest.String():
			w.Header().Set("Content-Type", string(ocispec.MediaTypeImageIndex))
			w.Header().Set("Content-Length", strconv.Itoa(len(indexBytes)))
			_, _ = w.Write(indexBytes)
		case "/v2/library/nginx/manifests/" + nydusDigest.String():
			w.Header().Set("Content-Type", string(ocispec.MediaTypeImageManifest))
			w.Header().Set("Content-Length", strconv.Itoa(len(manifestBytes)))
			_, _ = w.Write(manifestBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	hostDir := filepath.Join(root, "docker.io")
	assert.NoError(t, os.MkdirAll(hostDir, 0o755))
	hostsTOML := fmt.Sprintf(`
server = "https://registry-1.docker.io"

[host.%q]
  capabilities = ["pull", "resolve"]
  [host.%q.header]
    X-Dragonfly-Registry = %q
`, server.URL, server.URL, dragonflyRegistry)
	assert.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hostsTOML), 0o644))

	d := &detector{remote: remote.New(nil, false, remote.WithCertsDRoots(root))}
	desc, err := d.checkIndexAlternative(context.Background(), "docker.io/library/nginx:latest", originalDigest)
	require.NoError(t, err)
	assert.Equal(t, metaLayerDigest, desc.Digest)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{
		"HEAD /v2/library/nginx/manifests/latest",
		"GET /v2/library/nginx/manifests/" + indexDigest.String(),
		"GET /v2/library/nginx/manifests/" + nydusDigest.String(),
	}, requests)
}
