/*
 * Copyright (c) 2024. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filefs

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/log"
	"github.com/pkg/errors"
)

// DataFetcher handles on-demand data retrieval for file-backed EROFS mounts.
// It integrates with the existing blob cache infrastructure to fetch data
// ranges from local cache or remote registries.
type DataFetcher struct {
	cacheDirPath string
	mu           sync.RWMutex
	// Track files that have already been fully fetched to avoid redundant work.
	fetched map[string]bool
}

// NewDataFetcher creates a DataFetcher backed by the given cache directory.
func NewDataFetcher(cacheDirPath string) *DataFetcher {
	return &DataFetcher{
		cacheDirPath: cacheDirPath,
		fetched:      make(map[string]bool),
	}
}

// EnsureDataAvailable ensures that the data backing the given file path
// within the EROFS mount is available. If the data is already cached locally,
// this is a no-op. Otherwise, it fetches the data from the remote registry
// and populates the cache.
func (f *DataFetcher) EnsureDataAvailable(filePath, backingFile string) error {
	// Fast path: check if this file has already been fetched.
	f.mu.RLock()
	if f.fetched[filePath] {
		f.mu.RUnlock()
		return nil
	}
	f.mu.RUnlock()

	// Determine the blob ID from the file path within the mount.
	blobID := f.resolveBlobID(filePath)
	if blobID == "" {
		// Not a blob-backed file, allow access.
		return nil
	}

	// Check if the blob data exists in the local cache.
	cachePath := filepath.Join(f.cacheDirPath, blobID)
	if _, err := os.Stat(cachePath); err == nil {
		// Data is already cached locally.
		f.mu.Lock()
		f.fetched[filePath] = true
		f.mu.Unlock()
		return nil
	}

	// Fetch data from remote. This integrates with the existing registry
	// and auth infrastructure via the cache manager's blob download path.
	if err := f.fetchFromRemote(blobID, cachePath); err != nil {
		return errors.Wrapf(err, "fetch blob %s from remote", blobID)
	}

	f.mu.Lock()
	f.fetched[filePath] = true
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
// to the local cache directory.
func (f *DataFetcher) fetchFromRemote(blobID, cachePath string) error {
	// TODO: Integrate with pkg/remote and pkg/auth to download blob data
	// from the container registry. This should reuse the same auth and
	// transport infrastructure used by the fscache and fusedev drivers.
	//
	// The implementation should:
	// 1. Look up auth credentials via auth.GetRegistryKeyChain()
	// 2. Create a remote resolver
	// 3. Fetch the blob by digest
	// 4. Write the blob data to cachePath
	// 5. Support range requests for partial blob fetching
	return errors.Errorf("remote blob fetching not yet implemented for blob %s", blobID)
}
