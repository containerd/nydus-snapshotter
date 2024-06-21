/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package referrer

import (
	"context"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/golang/groupcache/lru"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
)

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

// CheckReferrer attempts to fetch the referrers and parse out
// the nydus image by specified manifest digest.
func (manager *Manager) CheckReferrer(ctx context.Context, ref string, manifestDigest digest.Digest) (*ocispec.Descriptor, error) {
	metaLayer, err, _ := manager.sg.Do(manifestDigest.String(), func() (interface{}, error) {
		// Try to get nydus metadata layer descriptor from LRU cache.
		if metaLayer, ok := manager.cache.Get(manifestDigest); ok {
			desc := metaLayer.(ocispec.Descriptor)
			return &desc, nil
		}

		keyChain, err := auth.GetKeyChainByRef(ref, nil)
		if err != nil {
			return nil, errors.Wrap(err, "get key chain")
		}

		// No LRU cache found, try to fetch referrers and parse out
		// the nydus metadata layer descriptor.
		referrer := newReferrer(keyChain, manager.insecure)
		metaLayer, err := referrer.checkReferrer(ctx, ref, manifestDigest)
		if err != nil {
			return nil, errors.Wrap(err, "check referrer")
		}

		// FIXME: how to invalidate the LRU cache if referrers update?
		manager.cache.Add(manifestDigest, *metaLayer)

		return metaLayer, nil
	})

	if err != nil {
		log.L.WithField("ref", ref).WithError(err).Warn("check referrer")
		return nil, err
	}

	return metaLayer.(*ocispec.Descriptor), nil
}

// TryFetchMetadata try to fetch and unpack nydus metadata file to specified path.
func (manager *Manager) TryFetchMetadata(ctx context.Context, ref string, manifestDigest digest.Digest, metadataPath string) error {
	metaLayer, err := manager.CheckReferrer(ctx, ref, manifestDigest)
	if err != nil {
		return errors.Wrap(err, "check referrer")
	}

	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return errors.Wrap(err, "get key chain")
	}

	referrer := newReferrer(keyChain, manager.insecure)
	return referrer.fetchMetadata(ctx, ref, *metaLayer, metadataPath)
}
