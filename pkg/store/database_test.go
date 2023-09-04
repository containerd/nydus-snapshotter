package store

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_daemon(t *testing.T) {
	rootDir := "testdata/snapshot"
	err := os.MkdirAll(rootDir, 0755)
	require.Nil(t, err)
	defer func() {
		_ = os.RemoveAll(rootDir)
	}()

	db, err := NewDatabase(rootDir)
	require.Nil(t, err)

	ctx := context.TODO()
	// Add daemons
	d1 := daemon.Daemon{States: daemon.ConfigState{ID: "d1"}}
	d2 := daemon.Daemon{States: daemon.ConfigState{ID: "d2"}}
	d3 := daemon.Daemon{States: daemon.ConfigState{ID: "d3"}}
	err = db.SaveDaemon(ctx, &d1)
	require.Nil(t, err)
	err = db.SaveDaemon(ctx, &d2)
	require.Nil(t, err)
	err = db.SaveDaemon(ctx, &d3)
	assert.Nil(t, err)
	require.Nil(t, err)
	// duplicate daemon id should fail
	err = db.SaveDaemon(ctx, &d1)
	require.Error(t, err)

	// Delete one daemon
	err = db.DeleteDaemon(ctx, "d2")
	require.Nil(t, err)

	// Check records
	ids := make(map[string]string)
	_ = db.WalkDaemons(ctx, func(info *daemon.ConfigState) error {
		ids[info.ID] = ""
		return nil
	})
	_, ok := ids["d1"]
	require.Equal(t, ok, true)
	_, ok = ids["d2"]
	require.Equal(t, ok, false)
	_, ok = ids["d3"]
	require.Equal(t, ok, true)

	// Cleanup records
	err = db.CleanupDaemons(ctx)
	require.Nil(t, err)
	ids2 := make([]string, 0)
	err = db.WalkDaemons(ctx, func(info *daemon.ConfigState) error {
		ids2 = append(ids2, info.ID)
		return nil
	})
	require.Nil(t, err)
	require.Equal(t, len(ids2), 0)
}

func TestLegacyRecordsMultipleDaemonModes(t *testing.T) {
	src, _ := os.Open("testdata/nydus_multiple_compat.db")

	defer src.Close()

	dst, _ := os.Create("testdata/nydus.db")

	t.Cleanup(func() {
		os.RemoveAll("testdata/nydus.db")
	})

	_, err := io.Copy(dst, src)
	require.Nil(t, err)
	dst.Close()
	_, err = NewDatabase("testdata")
	assert.Nil(t, err)

}

func TestLegacyRecordsSharedDaemonModes(t *testing.T) {
	src, _ := os.Open("testdata/nydus_shared_compat.db")

	defer src.Close()

	dst, _ := os.Create("testdata/nydus.db")

	t.Cleanup(func() {
		os.RemoveAll("testdata/nydus.db")
	})

	_, err := io.Copy(dst, src)
	require.Nil(t, err)
	dst.Close()
	_, err = NewDatabase("testdata")
	assert.Nil(t, err)
}
