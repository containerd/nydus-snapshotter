/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package rafs

import (
	"testing"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/internal/constant"
	"gotest.tools/assert"
)

func TestRafsSetEmpty(t *testing.T) {
	cache := NewRafsCache()

	assert.Assert(t, cache.Get("rafs1") == nil)
	assert.Equal(t, cache.Len(), 0)
	assert.Assert(t, cache.Head() == nil)
	instances := cache.List()
	assert.Equal(t, len(instances), 0)
}

func TestRafs(t *testing.T) {
	tmpDir := t.TempDir()
	snapshotterConfig := config.SnapshotterConfig{}
	snapshotterConfig.Root = tmpDir
	snapshotterConfig.DaemonMode = constant.DaemonModeDedicated
	assert.Assert(t, config.ProcessConfigurations(&snapshotterConfig))

	rafs, err := NewRafs("snapshot1", "image1", "fscache")
	assert.Assert(t, err)
	assert.Equal(t, rafs, RafsGlobalCache.Get("snapshot1"))
	assert.Equal(t, RafsGlobalCache.Len(), 1)
	assert.Equal(t, rafs, RafsGlobalCache.Head())
	instances := RafsGlobalCache.List()
	assert.Equal(t, len(instances), 1)
	assert.Equal(t, instances["snapshot1"].SnapshotID, "snapshot1")

	RafsGlobalCache.Lock()
	instances2 := RafsGlobalCache.ListLocked()
	RafsGlobalCache.Unlock()
	assert.Equal(t, len(instances2), 1)

	RafsGlobalCache.SetIntances(instances)
	assert.Equal(t, RafsGlobalCache.Len(), 1)
	assert.Equal(t, RafsGlobalCache.Head().SnapshotID, "snapshot1")

	assert.Equal(t, len(rafs.Annotations), 0)
	rafs.AddAnnotation("key", "value")
	assert.Equal(t, len(rafs.Annotations), 1)
	assert.Equal(t, rafs.GetSnapshotDir(), tmpDir+"/snapshots/snapshot1")
	assert.Equal(t, rafs.RelaMountpoint(), "/snapshot1")
	assert.Equal(t, rafs.FscacheWorkDir(), tmpDir+"/snapshots/snapshot1/fs")
	assert.Equal(t, rafs.GetFsDriver(), "fscache")
	rafs.SetMountpoint("/tmp/mnt")
	assert.Equal(t, rafs.GetMountpoint(), "/tmp/mnt")
	_, err = rafs.BootstrapFile()
	assert.Assert(t, err != nil)

	RafsGlobalCache.Remove("snapshot1")
	assert.Equal(t, RafsGlobalCache.Len(), 0)
}
