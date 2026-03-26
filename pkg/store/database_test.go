package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
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
	rootDir := t.TempDir()
	prepareCompatTestConfig(t, rootDir, config.FsDriverFusedev, config.DaemonModeDedicated)

	mount1 := filepath.Join(rootDir, "mounts", "daemon-1")
	mount2 := filepath.Join(rootDir, "mounts", "daemon-2")
	snapshotsDir := filepath.Join(rootDir, "snapshots")

	records := []*CompatDaemon{
		{
			ID:               "daemon-1",
			SnapshotID:       "snapshot-1",
			ConfigDir:        filepath.Join(rootDir, "config", "daemon-1"),
			SocketDir:        filepath.Join(rootDir, "socket", "daemon-1"),
			LogDir:           filepath.Join(rootDir, "logs", "daemon-1"),
			LogLevel:         "info",
			SnapshotDir:      snapshotsDir,
			Pid:              101,
			ImageID:          "image-1",
			FsDriver:         config.FsDriverFusedev,
			CustomMountPoint: &mount1,
		},
		{
			ID:               "daemon-2",
			SnapshotID:       "snapshot-2",
			ConfigDir:        filepath.Join(rootDir, "config", "daemon-2"),
			SocketDir:        filepath.Join(rootDir, "socket", "daemon-2"),
			LogDir:           filepath.Join(rootDir, "logs", "daemon-2"),
			LogLevel:         "debug",
			SnapshotDir:      snapshotsDir,
			Pid:              202,
			ImageID:          "image-2",
			FsDriver:         config.FsDriverFusedev,
			CustomMountPoint: &mount2,
		},
	}

	writeLegacyDatabase(t, rootDir, records)

	db, err := NewDatabase(rootDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	gotDaemons := listDaemons(t, db)
	require.Len(t, gotDaemons, len(records))
	for _, record := range records {
		got := gotDaemons[record.ID]
		require.Equal(t, record.ID, got.ID)
		require.Equal(t, record.Pid, got.ProcessID)
		require.Equal(t, path.Join(record.SocketDir, "api.sock"), got.APISocket)
		require.Equal(t, config.DaemonModeDedicated, got.DaemonMode)
		require.Equal(t, record.FsDriver, got.FsDriver)
		require.Equal(t, record.LogDir, got.LogDir)
		require.Equal(t, record.LogLevel, got.LogLevel)
		require.Equal(t, *record.CustomMountPoint, got.Mountpoint)
		require.Equal(t, record.ConfigDir, got.ConfigDir)
	}

	gotInstances := listRafsInstances(t, db)
	require.Len(t, gotInstances, len(records))
	for _, record := range records {
		got := gotInstances[record.SnapshotID]
		require.Equal(t, record.ImageID, got.ImageID)
		require.Equal(t, record.ID, got.DaemonID)
		require.Equal(t, path.Join(record.SnapshotDir, record.SnapshotID), got.SnapshotDir)
		require.Equal(t, path.Join(record.SnapshotDir, record.SnapshotID, "mnt"), got.Mountpoint)
	}
}

func TestLegacyRecordsSharedDaemonModes(t *testing.T) {
	rootDir := t.TempDir()
	prepareCompatTestConfig(t, rootDir, config.FsDriverFscache, config.DaemonModeShared)

	rootMountpoint := filepath.Join(rootDir, "mnt")
	snapshotsDir := filepath.Join(rootDir, "snapshots")
	configDir1 := filepath.Join(rootDir, "config", "instance-1")
	configDir2 := filepath.Join(rootDir, "config", "instance-2")

	writeConfigFile(t, filepath.Join(configDir1, "config.json"), []byte(`{"instance":"1"}`))
	writeConfigFile(t, filepath.Join(configDir2, "config.json"), []byte(`{"instance":"2"}`))

	records := []*CompatDaemon{
		{
			ID:             "instance-daemon-1",
			SnapshotID:     "snapshot-a",
			ConfigDir:      configDir1,
			LogDir:         filepath.Join(rootDir, "logs", "shared"),
			LogLevel:       "info",
			SnapshotDir:    snapshotsDir,
			Pid:            301,
			ImageID:        "image-a",
			FsDriver:       config.FsDriverFscache,
			RootMountPoint: &rootMountpoint,
		},
		{
			ID:             "instance-daemon-2",
			SnapshotID:     "snapshot-b",
			ConfigDir:      configDir2,
			LogDir:         filepath.Join(rootDir, "logs", "shared"),
			LogLevel:       "info",
			SnapshotDir:    snapshotsDir,
			Pid:            302,
			ImageID:        "image-b",
			FsDriver:       config.FsDriverFscache,
			RootMountPoint: &rootMountpoint,
		},
		{
			ID:             SharedNydusDaemonID,
			SnapshotDir:    snapshotsDir,
			Pid:            303,
			FsDriver:       config.FsDriverFscache,
			LogDir:         filepath.Join(rootDir, "logs", "shared"),
			LogLevel:       "debug",
			RootMountPoint: &rootMountpoint,
		},
	}

	writeLegacyDatabase(t, rootDir, records)

	db, err := NewDatabase(rootDir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	gotDaemons := listDaemons(t, db)
	require.Len(t, gotDaemons, 1)

	sharedDaemon := gotDaemons[SharedNydusDaemonID]
	require.Equal(t, SharedNydusDaemonID, sharedDaemon.ID)
	require.Equal(t, 303, sharedDaemon.ProcessID)
	require.Equal(t, path.Join(snapshotsDir, "api.sock"), sharedDaemon.APISocket)
	require.Equal(t, config.DaemonModeShared, sharedDaemon.DaemonMode)
	require.Equal(t, config.FsDriverFscache, sharedDaemon.FsDriver)
	require.Equal(t, rootMountpoint, sharedDaemon.Mountpoint)
	require.Equal(t, filepath.Join(rootDir, "config", SharedNydusDaemonID), sharedDaemon.ConfigDir)

	gotInstances := listRafsInstances(t, db)
	require.Len(t, gotInstances, 2)
	for _, record := range records[:2] {
		got := gotInstances[record.SnapshotID]
		require.Equal(t, record.ImageID, got.ImageID)
		require.Equal(t, SharedNydusDaemonID, got.DaemonID)
		require.Equal(t, path.Join(record.SnapshotDir, record.SnapshotID), got.SnapshotDir)
		require.Equal(t, path.Join(rootMountpoint, record.SnapshotID), got.Mountpoint)
	}

	require.FileExists(t, filepath.Join(rootDir, "config", SharedNydusDaemonID, "config.json"))
	require.FileExists(t, filepath.Join(rootDir, "config", SharedNydusDaemonID, "snapshot-a", "config.json"))
	require.FileExists(t, filepath.Join(rootDir, "config", SharedNydusDaemonID, "snapshot-b", "config.json"))
}

func prepareCompatTestConfig(t *testing.T, rootDir, fsDriver string, daemonMode config.DaemonMode) {
	t.Helper()

	cfg := &config.SnapshotterConfig{
		Root:       rootDir,
		DaemonMode: string(daemonMode),
		DaemonConfig: config.DaemonConfig{
			FsDriver: fsDriver,
		},
	}

	require.NoError(t, config.ProcessConfigurations(cfg))
}

func writeLegacyDatabase(t *testing.T, rootDir string, records []*CompatDaemon) {
	t.Helper()

	db, err := bolt.Open(filepath.Join(rootDir, databaseFileName), 0600, &bolt.Options{Timeout: 4 * time.Second})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, db.Close())
	}()

	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(daemonsBucket)
		if err != nil {
			return err
		}

		for i, record := range records {
			payload, err := json.Marshal(record)
			if err != nil {
				return err
			}

			key := fmt.Sprintf("%02d-%s", i, record.ID)
			if err := bucket.Put([]byte(key), payload); err != nil {
				return err
			}
		}

		return nil
	})
	require.NoError(t, err)
}

func writeConfigFile(t *testing.T, file string, content []byte) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0755))
	require.NoError(t, os.WriteFile(file, content, 0644))
}

func listDaemons(t *testing.T, db *Database) map[string]daemon.ConfigState {
	t.Helper()

	daemons := make(map[string]daemon.ConfigState)
	err := db.WalkDaemons(context.TODO(), func(info *daemon.ConfigState) error {
		daemons[info.ID] = *info
		return nil
	})
	require.NoError(t, err)

	return daemons
}

func listRafsInstances(t *testing.T, db *Database) map[string]rafs.Rafs {
	t.Helper()

	instances := make(map[string]rafs.Rafs)
	err := db.WalkRafsInstances(context.TODO(), func(instance *rafs.Rafs) error {
		instances[instance.SnapshotID] = *instance
		return nil
	})
	require.NoError(t, err)

	return instances
}
