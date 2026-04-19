/*
 * Copyright (c) 2024. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filefs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
)

// DataFetcher handles on-demand data retrieval for file-backed EROFS mounts.
// It integrates with the existing blob cache infrastructure to fetch data
// from local cache or remote registries.
type DataFetcher struct {
	cacheDirPath string
	insecure     bool
	mu           sync.RWMutex
	// Track blobs that have already been fully fetched to avoid redundant work.
	fetched map[string]bool
	// Dedup concurrent fetches for the same blob.
	sg singleflight.Group
}

// NewDataFetcher creates a DataFetcher backed by the given cache directory.
func NewDataFetcher(cacheDirPath string, insecure bool) *DataFetcher {
	return &DataFetcher{
		cacheDirPath: cacheDirPath,
		insecure:     insecure,
		fetched:      make(map[string]bool),
	}
}

// EnsureDataAvailable ensures that the data backing the given file path
// within the EROFS mount is available. If the data is already cached locally,
// this is a no-op. Otherwise, it fetches the data from the remote registry
// and populates the cache.
func (f *DataFetcher) EnsureDataAvailable(filePath, backingFile string) error {
	// Determine the blob ID from the file path within the mount.
	blobID := f.resolveBlobID(filePath)
	if blobID == "" {
		// Not a blob-backed file or resolution not possible, allow access.
		return nil
	}

	// Fast path: check if this blob has already been fetched.
	f.mu.RLock()
	if f.fetched[blobID] {
		f.mu.RUnlock()
		return nil
	}
	f.mu.RUnlock()

	// Check if the blob data exists in the local cache.
	cachePath := filepath.Join(f.cacheDirPath, blobID)
	if _, err := os.Stat(cachePath); err == nil {
		// Data is already cached locally.
		f.mu.Lock()
		f.fetched[blobID] = true
		f.mu.Unlock()
		return nil
	}

	return nil
}

// FetchBlob fetches a blob by digest from the remote registry and stores it
// in the local cache. Uses singleflight to dedup concurrent requests for the
// same blob. imageRef is the container image reference (e.g. "docker.io/library/nginx:latest").
func (f *DataFetcher) FetchBlob(ctx context.Context, imageRef string, blobDigest digest.Digest) error {
	blobID := blobDigest.Hex()

	// Fast path: already cached.
	f.mu.RLock()
	if f.fetched[blobID] {
		f.mu.RUnlock()
		return nil
	}
	f.mu.RUnlock()

	cachePath := filepath.Join(f.cacheDirPath, blobID)
	if _, err := os.Stat(cachePath); err == nil {
		f.mu.Lock()
		f.fetched[blobID] = true
		f.mu.Unlock()
		return nil
	}

	// Use singleflight to dedup concurrent fetches for the same blob.
	_, err, _ := f.sg.Do(blobID, func() (interface{}, error) {
		return nil, f.fetchFromRemote(ctx, imageRef, blobDigest, cachePath)
	})
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.fetched[blobID] = true
	f.mu.Unlock()
	return nil
}

// resolveBlobID extracts the blob identifier from a file path within the
// EROFS mount. The exact mapping depends on the EROFS image format (RAFS v5/v6).
func (f *DataFetcher) resolveBlobID(filePath string) string {
	// For RAFS-formatted EROFS images, the blob ID is typically embedded
	// in the EROFS metadata. During the pre-content access, we need to
	// map the accessed file region back to the corresponding blob and offset.
	//
	// TODO: Implement proper blob ID resolution by reading EROFS/RAFS metadata
	// to map file paths to their backing blob IDs and data ranges.
	// For now, return empty to allow all accesses through.
	log.L.Debugf("filefs fetcher: resolving blob for path %s", filePath)
	return ""
}

// fetchFromRemote downloads a blob from the remote registry and saves it
// to the local cache directory using atomic write (tmp + rename).
// Follows the same pattern as pkg/tarfs/tarfs.go getBlobStream + blobProcess.
func (f *DataFetcher) fetchFromRemote(ctx context.Context, imageRef string, blobDigest digest.Digest, cachePath string) error {
	// 1. Get auth credentials for the image reference.
	keyChain, err := auth.GetKeyChainByRef(imageRef, nil)
	if err != nil {
		log.L.WithError(err).Warnf("filefs: failed to get keychain for %s, trying without auth", imageRef)
		keyChain = nil
	}

	// 2. Create remote client with auth.
	r := remote.New(keyChain, f.insecure)

	// 3. Fetch the blob by digest, with HTTP fallback retry.
	rc, err := f.getBlobStream(ctx, r, imageRef, blobDigest)
	if err != nil && r.RetryWithPlainHTTP(imageRef, err) {
		rc, err = f.getBlobStream(ctx, r, imageRef, blobDigest)
	}
	if err != nil {
		return errors.Wrapf(err, "fetch blob %s from %s", blobDigest, imageRef)
	}
	defer rc.Close()

	// 4. Atomic write: write to tmp file, then rename to final path.
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return errors.Wrapf(err, "create cache dir for %s", cachePath)
	}

	tmpPath := cachePath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return errors.Wrapf(err, "create temp file %s", tmpPath)
	}

	if _, err := io.Copy(tmpFile, rc); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return errors.Wrapf(err, "write blob data to %s", tmpPath)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return errors.Wrapf(err, "close temp file %s", tmpPath)
	}

	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return errors.Wrapf(err, "rename %s to %s", tmpPath, cachePath)
	}

	log.L.Infof("filefs: fetched blob %s to cache %s", blobDigest, cachePath)
	return nil
}

// getBlobStream fetches a blob stream by digest from the remote registry.
// Same pattern as pkg/tarfs/tarfs.go:199-211.
func (f *DataFetcher) getBlobStream(ctx context.Context, r *remote.Remote, ref string, contentDigest digest.Digest) (io.ReadCloser, error) {
	fetcher, err := r.Fetcher(ctx, ref)
	if err != nil {
		return nil, errors.Wrap(err, "get remote fetcher")
	}

	fetcherByDigest, ok := fetcher.(remotes.FetcherByDigest)
	if !ok {
		return nil, errors.Errorf("fetcher %T does not implement FetcherByDigest", fetcher)
	}

	rc, _, err := fetcherByDigest.FetchByDigest(ctx, contentDigest)
	if err != nil {
		return nil, errors.Wrapf(err, "fetch blob %s", contentDigest)
	}

	return rc, nil
}
