/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractBlobIDFromFilename(t *testing.T) {
	tests := map[string]struct {
		filename string
		expected string
	}{
		"plain blob ID": {
			filename: "abc123def456",
			expected: "abc123def456",
		},
		"blob with .blob.data suffix": {
			filename: "abc123def456.blob.data",
			expected: "abc123def456",
		},
		"blob with .chunk_map suffix": {
			filename: "abc123def456.chunk_map",
			expected: "abc123def456",
		},
		"blob with .blob.meta suffix": {
			filename: "abc123def456.blob.meta",
			expected: "abc123def456",
		},
		"blob with .image.disk suffix": {
			filename: "abc123def456.image.disk",
			expected: "abc123def456",
		},
		"blob with .layer.disk suffix": {
			filename: "abc123def456.layer.disk",
			expected: "abc123def456",
		},
		"blob with combined .blob.data.chunk_map suffix": {
			filename: "abc123def456.blob.data.chunk_map",
			expected: "abc123def456",
		},
		"real sha256 hash": {
			filename: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"real sha256 hash with .blob.data": {
			filename: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.blob.data",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"real sha256 hash with .chunk_map": {
			filename: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.chunk_map",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"real sha256 hash with .blob.data.chunk_map": {
			filename: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855.blob.data.chunk_map",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"empty filename": {
			filename: "",
			expected: "",
		},
		"filename with unknown suffix": {
			filename: "abc123def456.unknown",
			expected: "abc123def456.unknown",
		},
		"filename with multiple dots but no known suffix": {
			filename: "abc.def.ghi",
			expected: "abc.def.ghi",
		},
		".blob.data.chunk_map is matched before .blob.data or .chunk_map": {
			filename: "test.blob.data.chunk_map",
			expected: "test",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := ExtractBlobIDFromFilename(tc.filename)
			assert.Equal(t, tc.expected, result)
		})
	}
}
