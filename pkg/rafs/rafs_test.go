/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package rafs

import (
	"sync"
	"testing"
)

// Exercises UpdateUnderlyingFiles concurrently with the deep-copying List()
// readers of both registries that hand out the same *Rafs. Meaningful under
// the race detector: the writer used to hold only the daemon cache's mutex,
// racing with RafsGlobalCache.List() (e.g. snapshot cleanup).
func TestUpdateUnderlyingFilesConcurrentWithListReaders(t *testing.T) {
	original := RafsGlobalCache.List()
	RafsGlobalCache.SetIntances(make(map[string]*Rafs))
	t.Cleanup(func() {
		RafsGlobalCache.SetIntances(original)
	})

	instance := &Rafs{
		SnapshotID:  "1",
		Annotations: map[string]string{},
	}
	RafsGlobalCache.Add(instance)
	daemonCache := NewRafsCache()
	daemonCache.Add(instance)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := range 1000 {
			files := []string{"/cache/blob-a", "/cache/blob-b"}[:1+i%2]
			instance.UpdateUnderlyingFiles(files, &daemonCache)
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			for _, r := range RafsGlobalCache.List() {
				_ = len(r.UnderlyingFiles)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			for _, r := range daemonCache.List() {
				_ = len(r.UnderlyingFiles)
			}
		}
	}()
	wg.Wait()
}
