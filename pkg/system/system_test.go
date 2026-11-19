/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/store"
)

func TestBuildUpgradeSocket(t *testing.T) {
	cur := "api.sock"

	next, err := buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, next, "api1.sock")

	cur = "api2.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api3.sock", next)

	cur = "api23.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api24.sock", next)

	cur = "api222.sock"

	next, err = buildNextAPISocket(cur)
	assert.Nil(t, err)
	assert.Equal(t, "api223.sock", next)
}

func TestJSONResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	jsonResponse(rec, map[string]string{"key": "value"})

	// Result() reports the headers as of WriteHeader, i.e. what is actually sent.
	resp := rec.Result()
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"key":"value"}`, rec.Body.String())
}

func newTestController(t *testing.T) (*Controller, *store.Database) {
	t.Helper()

	dir := t.TempDir()
	db, err := store.NewDatabase(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	sc, err := NewSystemController(nil, nil, db, filepath.Join(dir, "system.sock"), 0, 0)
	require.NoError(t, err)

	return sc, db
}

func getRecords(t *testing.T, sc *Controller) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, endpointDaemonRecords, nil)
	rec := httptest.NewRecorder()
	sc.router.ServeHTTP(rec, req)

	return rec
}

func TestGetDaemonRecords(t *testing.T) {
	sc, db := newTestController(t)

	ctx := context.Background()
	daemons := []daemon.ConfigState{
		{
			ID:         "d1",
			ProcessID:  1001,
			APISocket:  "/run/nydus/socket/d1/api.sock",
			DaemonMode: config.DaemonModeDedicated,
			FsDriver:   config.FsDriverFusedev,
			Mountpoint: "/run/nydus/mnt/d1",
		},
		{
			ID:         "d2",
			ProcessID:  1002,
			DaemonMode: config.DaemonModeShared,
			FsDriver:   config.FsDriverFscache,
		},
	}
	for _, s := range daemons {
		require.NoError(t, db.SaveDaemon(ctx, &daemon.Daemon{States: s}))
	}

	instances := []rafs.Rafs{
		{
			Seq:         1,
			ImageID:     "docker.io/library/busybox:latest",
			DaemonID:    "d1",
			FsDriver:    config.FsDriverFusedev,
			SnapshotID:  "10",
			SnapshotDir: "/var/lib/nydus/snapshots/10",
			Mountpoint:  "/run/nydus/mnt/d1",
		},
		{
			Seq:        2,
			ImageID:    "docker.io/library/nginx:latest",
			DaemonID:   "d2",
			FsDriver:   config.FsDriverFscache,
			SnapshotID: "20",
			Annotations: map[string]string{
				rafs.AnnoFsCacheID: "fscache-id",
			},
		},
	}
	for i := range instances {
		require.NoError(t, db.AddRafsInstance(ctx, &instances[i]))
	}

	rec := getRecords(t, sc)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	resp := rec.Result()
	defer resp.Body.Close()
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var records daemonRecords
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &records))

	require.Len(t, records.Daemons, len(daemons))
	for i := range daemons {
		assert.Equal(t, daemons[i], *records.Daemons[i])
	}

	require.Len(t, records.Instances, len(instances))
	for i := range instances {
		assert.Equal(t, instances[i], *records.Instances[i])
	}
}

func TestGetDaemonRecordsEmpty(t *testing.T) {
	sc, _ := newTestController(t)
	rec := getRecords(t, sc)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"daemons":[],"instances":[]}`, rec.Body.String())
}

func TestGetDaemonRecordsReadFailure(t *testing.T) {
	sc, db := newTestController(t)
	require.NoError(t, db.Close())

	rec := getRecords(t, sc)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var m errorMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	assert.Equal(t, defaultErrorCode, m.Code)
	assert.Contains(t, m.Message, "walk daemon records")
}
