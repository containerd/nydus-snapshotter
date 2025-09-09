/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package index

import (
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
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
