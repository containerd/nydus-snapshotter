/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package stargz

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/process"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

const dataBaseDir = "db"

func ensureExists(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s not exists", path)
	}
	return nil
}

func Test_filesystem_createNewDaemon(t *testing.T) {
	snapshotRoot := "testdata/snapshot"
	err := os.MkdirAll(snapshotRoot, 0755)
	require.Nil(t, err)
	defer func() {
		_ = os.RemoveAll(snapshotRoot)
	}()

	databaseDir := path.Join("testdata", dataBaseDir)
	db, err := store.NewDatabase(databaseDir)
	assert.Nil(t, err)
	// Close db to release database flock
	defer db.Close()

	mgr, err := process.NewManager(process.Opt{
		NydusdBinaryPath: "",
		Database:         db,
	})
	require.Nil(t, err)

	f := Filesystem{
		FileSystemMeta: meta.FileSystemMeta{
			RootDir: snapshotRoot,
		},
		manager:     mgr,
		daemonCfg:   config.DaemonConfig{},
		resolver:    nil,
		vpcRegistry: false,
	}
	_, err = f.createNewDaemon("1", "example.com/test/testimage:0.1")
	require.Nil(t, err)
}

func Test_filesystem_generateDaemonConfig(t *testing.T) {
	snapshotRoot := "testdata/snapshot"
	err := os.MkdirAll(snapshotRoot, 0755)
	require.Nil(t, err)
	defer func() {
		_ = os.RemoveAll(snapshotRoot)
	}()

	content, err := ioutil.ReadFile("testdata/config/nydus.json")
	require.Nil(t, err)
	var cfg config.DaemonConfig
	err = json.Unmarshal(content, &cfg)
	require.Nil(t, err)

	databaseDir := path.Join("testdata", dataBaseDir)
	db, err := store.NewDatabase(databaseDir)
	assert.Nil(t, err)

	mgr, err := process.NewManager(process.Opt{
		NydusdBinaryPath: "",
		Database:         db,
	})
	require.Nil(t, err)

	f := Filesystem{
		FileSystemMeta: meta.FileSystemMeta{
			RootDir: snapshotRoot,
		},
		manager:     mgr,
		daemonCfg:   cfg,
		resolver:    nil,
		vpcRegistry: false,
	}
	d, err := f.createNewDaemon("1", "example.com/test/testimage:0.1")
	assert.Nil(t, err)
	err = f.generateDaemonConfig(d, map[string]string{
		label.ImagePullUsername: "mock",
		label.ImagePullSecret:   "mock",
	})
	require.Nil(t, err)
	assert.Nil(t, ensureExists(filepath.Join(snapshotRoot, "config", d.ID, "config.json")))
	assert.Nil(t, ensureExists(filepath.Join(snapshotRoot, "socket", d.ID)))
}
