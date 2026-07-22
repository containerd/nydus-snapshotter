/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	racache "github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

func TestTryRetainSharedDaemonReplacesUpgradedDaemon(t *testing.T) {
	fs := &Filesystem{}

	oldDaemon := &daemon.Daemon{
		States: daemon.ConfigState{
			ID:       "shared-daemon",
			FsDriver: config.FsDriverFscache,
		},
	}
	fs.TryRetainSharedDaemon(oldDaemon)
	require.Same(t, oldDaemon, fs.fscacheSharedDaemon)
	require.Equal(t, int32(1), oldDaemon.GetRef())

	newDaemon := &daemon.Daemon{
		States: daemon.ConfigState{
			ID:       "shared-daemon",
			FsDriver: config.FsDriverFscache,
		},
	}
	newDaemon.IncRef()
	fs.TryRetainSharedDaemon(newDaemon)

	require.Same(t, newDaemon, fs.fscacheSharedDaemon)
	require.Equal(t, int32(1), oldDaemon.GetRef())
	require.Equal(t, int32(1), newDaemon.GetRef())
}

// RefreshUnderlyingFiles is called from the blob cache GC path for every
// instance with unknown usage, so a daemon whose API socket is gone must make
// it fail immediately rather than sit in the 10-second socket poll that
// d.GetClient performs.
func TestRefreshUnderlyingFilesFailsFastWithoutDaemonSocket(t *testing.T) {
	rootDir := t.TempDir()
	db, err := store.NewDatabase(rootDir)
	require.NoError(t, err)

	m, err := manager.NewManager(manager.Opt{
		Database:      db,
		RootDir:       rootDir,
		FsDriver:      config.FsDriverFusedev,
		RecoverPolicy: config.RecoverPolicyRestart,
	})
	require.NoError(t, err)

	d, err := daemon.NewDaemon(
		daemon.WithSocketDir(rootDir),
		daemon.WithConfigDir(rootDir),
		daemon.WithLogDir(rootDir),
		daemon.WithFsDriver(config.FsDriverFusedev),
		daemon.WithDaemonMode(config.DaemonModeDedicated),
	)
	require.NoError(t, err)
	require.NoError(t, m.AddDaemon(d))

	fs := &Filesystem{enabledManagers: map[string]*manager.Manager{config.FsDriverFusedev: m}}
	instance := &racache.Rafs{
		SnapshotID: "snap-1",
		DaemonID:   d.ID(),
		FsDriver:   config.FsDriverFusedev,
	}

	start := time.Now()
	err = fs.RefreshUnderlyingFiles(instance)
	require.True(t, errdefs.IsNotFound(err))
	require.Less(t, time.Since(start), 5*time.Second)
	require.Empty(t, instance.UnderlyingFiles)
}

func TestFscacheLifecycleAllowsConcurrentPublicationAndSkipsGC(t *testing.T) {
	fs := &Filesystem{}
	endFirst := fs.BeginFscachePublication([]string{"blob-a"})
	secondStarted := make(chan func(bool), 1)
	go func() {
		secondStarted <- fs.BeginFscachePublication([]string{"blob-a"})
	}()
	var endSecond func(bool)
	select {
	case endSecond = <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("mounts publishing the same blob must remain concurrent")
	}

	scanEpoch, endScan := fs.BeginFscacheGCScan()
	defer endScan()
	if _, ok := fs.TryBeginFscacheBlobGC("blob-a", scanEpoch); ok {
		t.Fatal("GC must skip a blob with active mount publication")
	}

	endFirst(true)
	endSecond(true)
	if _, ok := fs.TryBeginFscacheBlobGC("blob-a", scanEpoch); ok {
		t.Fatal("GC must skip a blob published after its scan started")
	}
}

func TestFscacheLifecycleOnlyBlocksMatchingPublicationDuringCull(t *testing.T) {
	fs := &Filesystem{}
	scanEpoch, endScan := fs.BeginFscacheGCScan()
	defer endScan()
	endBlobGC, ok := fs.TryBeginFscacheBlobGC("blob-a", scanEpoch)
	require.True(t, ok)

	matchingStarted := make(chan func(bool), 1)
	go func() {
		matchingStarted <- fs.BeginFscachePublication([]string{"blob-a"})
	}()
	select {
	case <-matchingStarted:
		t.Fatal("matching publication must wait while cull is active")
	case <-time.After(50 * time.Millisecond):
	}

	unrelatedStarted := fs.BeginFscachePublication([]string{"blob-b"})
	unrelatedStarted(false)

	endBlobGC()
	select {
	case endPublication := <-matchingStarted:
		endPublication(true)
	case <-time.After(time.Second):
		t.Fatal("matching publication did not resume after cull ended")
	}
}

func TestFscacheLifecycleWaitingPublicationPreventsAdditionalClaims(t *testing.T) {
	fs := &Filesystem{}
	scanEpoch, endScan := fs.BeginFscacheGCScan()
	defer endScan()
	endFirstGC, ok := fs.TryBeginFscacheBlobGC("blob-a", scanEpoch)
	require.True(t, ok)

	publicationStarted := make(chan func(bool), 1)
	go func() {
		publicationStarted <- fs.BeginFscachePublication([]string{"blob-a", "blob-b"})
	}()
	select {
	case <-publicationStarted:
		t.Fatal("publication must wait for the cull already active on blob-a")
	case <-time.After(50 * time.Millisecond):
	}

	if _, ok := fs.TryBeginFscacheBlobGC("blob-b", scanEpoch); ok {
		t.Fatal("waiting publication must prevent GC from claiming another shared blob")
	}
	endFirstGC()

	select {
	case endPublication := <-publicationStarted:
		endPublication(true)
	case <-time.After(time.Second):
		t.Fatal("publication did not resume after the active cull ended")
	}
}

func TestFscacheLifecycleDoesNotProtectPastPublicationFromLaterScan(t *testing.T) {
	fs := &Filesystem{}
	endPublication := fs.BeginFscachePublication([]string{"blob-a"})
	endPublication(true)

	scanEpoch, endScan := fs.BeginFscacheGCScan()
	defer endScan()
	endBlobGC, ok := fs.TryBeginFscacheBlobGC("blob-a", scanEpoch)
	require.True(t, ok, "a later scan must be able to reclaim an unused published blob")
	endBlobGC()
}

func TestRemoveCacheKeepsMetadataUntilCullCompletes(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")
	var statusCode atomic.Int32
	statusCode.Store(http.StatusOK)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v2/blobs/cull", r.URL.Path)
		require.Empty(t, r.URL.Query().Get("background"))
		status := int(statusCode.Load())
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"status":"pending","reason":"awaiting cachefiles commit"}`))
		}
	}))
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	ts.Listener = listener
	ts.Start()
	defer ts.Close()

	cacheDir := filepath.Join(dir, "cache")
	cacheMgr, err := cache.NewManager(cache.Opt{CacheDir: cacheDir})
	require.NoError(t, err)
	sharedDaemon := &daemon.Daemon{States: daemon.ConfigState{APISocket: sock}}
	fs := &Filesystem{
		fscacheSharedDaemon: sharedDaemon,
		enabledManagers: map[string]*manager.Manager{
			config.FsDriverFscache: {},
		},
		cacheMgr: cacheMgr,
	}

	blobID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	metaPath := filepath.Join(cacheDir, blobID+".blob.meta")
	require.NoError(t, os.WriteFile(metaPath, []byte("metadata"), 0o644))

	done, err := fs.RemoveCache("sha256:" + blobID)
	require.NoError(t, err)
	require.False(t, done)
	_, err = os.Stat(metaPath)
	require.NoError(t, err, "pending cull must retain metadata for the next GC scan")

	statusCode.Store(http.StatusNoContent)
	done, err = fs.RemoveCache("sha256:" + blobID)
	require.NoError(t, err)
	require.True(t, done)
	_, err = os.Stat(metaPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}
