//go:build !windows
// +build !windows

/*
 * Copyright (c) 2024. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
)

// d returns a synthetic digest for testing by repeating a hex character.
func d(c byte) digest.Digest {
	return digest.NewDigestFromEncoded(digest.SHA256, string(repeat(c, 64)))
}

func repeat(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}

// TestMergeManifestBlobDigests verifies the blob digest selection logic used when
// building the nydus manifest layer list.
func TestMergeManifestBlobDigests(t *testing.T) {
	D0 := d('0')
	D1 := d('1')
	D2 := d('2')
	D3 := d('3') // metadata-only layer (symlink-only, 0-byte blob data)
	DICT := d('d')

	tests := []struct {
		name                string
		nydusBlobDigests    []digest.Digest
		originalBlobDigests []digest.Digest
		expected            []digest.Digest
	}{
		{
			// A metadata-only layer (e.g. symlink-only OCI layer) produces an empty
			// blob inside nydus-image merge, so it is absent from originalBlobDigests.
			// mergeManifestBlobDigests must preserve it from nydusBlobDigests so that
			// reverse conversion (nydus→OCI) reproduces the correct number of layers.
			name:                "metadata-only layer preserved from nydusBlobDigests",
			nydusBlobDigests:    []digest.Digest{D0, D1, D2, D3},
			originalBlobDigests: []digest.Digest{D2, D0, D1}, // BFS order, D3 missing
			expected:            []digest.Digest{D0, D1, D2, D3},
		},
		{
			// When a chunk-dict is used, nydus-image merge adds the dict blob to its
			// output blob table. That dict blob is not a regular OCI layer, so it does
			// not appear in nydusBlobDigests and must be appended at the end.
			name:                "chunk-dict blob appended after layer blobs",
			nydusBlobDigests:    []digest.Digest{D0, D1, D2, D3},
			originalBlobDigests: []digest.Digest{D0, D1, D2, DICT},
			expected:            []digest.Digest{D0, D1, D2, D3, DICT},
		},
		{
			// When chunk-dict is used and layers are also reordered by BFS, both the
			// OCI layer order must be preserved and the dict blob must be appended.
			name:                "chunk-dict blob appended when originalBlobDigests uses BFS order",
			nydusBlobDigests:    []digest.Digest{D0, D1, D2, D3},
			originalBlobDigests: []digest.Digest{D2, D0, D1, DICT}, // BFS order + dict, D3 missing
			expected:            []digest.Digest{D0, D1, D2, D3, DICT},
		},
		{
			// When all layers have chunk data, originalBlobDigests covers all of
			// nydusBlobDigests (possibly in different BFS order). The result must equal
			// nydusBlobDigests in OCI layer order without duplicates.
			name:                "all layers have data, OCI order preserved",
			nydusBlobDigests:    []digest.Digest{D0, D1, D2},
			originalBlobDigests: []digest.Digest{D2, D0, D1}, // BFS order
			expected:            []digest.Digest{D0, D1, D2},
		},
		{
			name:                "empty inputs produce empty result",
			nydusBlobDigests:    []digest.Digest{},
			originalBlobDigests: []digest.Digest{},
			expected:            []digest.Digest{},
		},
		{
			name:                "single metadata-only layer",
			nydusBlobDigests:    []digest.Digest{D0},
			originalBlobDigests: []digest.Digest{},
			expected:            []digest.Digest{D0},
		},
		{
			// Multiple chunk-dict blobs (edge case; not typical but must not be dropped).
			name:                "multiple chunk-dict blobs appended",
			nydusBlobDigests:    []digest.Digest{D0, D1},
			originalBlobDigests: []digest.Digest{D0, D1, D2, D3},
			expected:            []digest.Digest{D0, D1, D2, D3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeManifestBlobDigests(tc.nydusBlobDigests, tc.originalBlobDigests)
			assert.Equal(t, tc.expected, got)
		})
	}
}
