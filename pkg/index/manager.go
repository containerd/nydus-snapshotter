/*
 * Copyright (c) 2025. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package index

import (
	"context"

	"github.com/containerd/log"
	"github.com/golang/groupcache/lru"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
)

var ErrNoNydusAlternative = errors.New("no alternative nydus descriptor found in index")

type Manager struct {
	insecure bool
	cache    *lru.Cache
	sg       singleflight.Group
}

func NewManager(insecure bool) *Manager {
	manager := Manager{
		insecure: insecure,
		cache:    lru.New(500),
		sg:       singleflight.Group{},
	}

	return &manager
}

// CheckIndexAlternative attempts to find a nydus alternative manifest
// within an OCI index manifest for the specified manifest digest.
func (manager *Manager) CheckIndexAlternative(ctx context.Context, ref string, manifestDigest digest.Digest) (*ocispec.Descriptor, error) {
	nydusDesc, err, _ := manager.sg.Do(manifestDigest.String(), func() (interface{}, error) {
		// Try to get nydus metadata layer descriptor from LRU cache.
		desc, ok := manager.cache.Get(manifestDigest)
		if ok {
			metaLayer, ok := desc.(ocispec.Descriptor)
			if ok {
				return &metaLayer, nil
			}
			return nil, ErrNoNydusAlternative
		}

		keyChain, err := auth.GetKeyChainByRef(ref, nil)
		if err != nil {
			return nil, errors.Wrap(err, "get key chain")
		}

		// No LRU cache found, try to detect nydus alternative in index manifest.
		detector := newDetector(keyChain, manager.insecure)
		metaLayer, err := detector.checkIndexAlternative(ctx, ref, manifestDigest)
		if err != nil {
			// Cache empty result to avoid repeated failures.
			// The index manifest can't change as it would change its digest so checking once is enough
			manager.cache.Add(manifestDigest, nil)
			return nil, errors.Wrap(err, "check index alternative")
		}

		// Cache the result for future use
		manager.cache.Add(manifestDigest, *metaLayer)

		return metaLayer, nil
	})

	logger := log.G(ctx).WithField("ref", ref).WithField("digest", manifestDigest.String())
	if err == ErrNoNydusAlternative {
		return nil, err
	} else if err != nil {
		logger.WithError(err).Warn("index detection failed")
		return nil, err
	}

	return nydusDesc.(*ocispec.Descriptor), nil
}

// TryFetchMetadata try to fetch and unpack nydus metadata file to specified path.
func (manager *Manager) TryFetchMetadata(ctx context.Context, ref string, manifestDigest digest.Digest, metadataPath string) error {
	metaLayer, err := manager.CheckIndexAlternative(ctx, ref, manifestDigest)
	if err != nil {
		return errors.Wrap(err, "check index alternative")
	}

	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return errors.Wrap(err, "get key chain")
	}

	detector := newDetector(keyChain, manager.insecure)
	return detector.fetchMetadata(ctx, ref, *metaLayer, metadataPath)
}
