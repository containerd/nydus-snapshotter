/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package index

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
)

func TestCheckIndexAlternative(t *testing.T) {
	manifestDigest := digest.FromString("test-manifest")
	ref := "registry.example.com/test/repo:latest"

	expectedDesc := &ocispec.Descriptor{
		Digest: digest.FromString("meta-layer"),
	}

	t.Run("cache hit, nydus present", func(t *testing.T) {
		manager := NewManager(false)
		manager.cache.Add(manifestDigest, *expectedDesc)

		result, err := manager.CheckIndexAlternative(context.Background(), ref, manifestDigest)

		assert.NoError(t, err)
		assert.Equal(t, expectedDesc, result)
	})

	t.Run("cache hit, no nydus", func(t *testing.T) {
		manager := NewManager(false)
		manager.cache.Add(manifestDigest, nil)

		_, err := manager.CheckIndexAlternative(context.Background(), ref, manifestDigest)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no alternative nydus descriptor found in index")
	})
}
