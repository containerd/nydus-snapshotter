/*
 * Copyright (c) 2024. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeMirrorHostsToml(t *testing.T, dir, registryHost, content string) {
	t.Helper()
	hostDir := filepath.Join(dir, registryHost)
	require.NoError(t, os.MkdirAll(hostDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(content), 0600))
}

func TestSplitMirrorURL(t *testing.T) {
	cases := []struct {
		input          string
		expectedScheme string
		expectedHost   string
	}{
		{"http://mirror:5000", "http", "mirror:5000"},
		{"https://mirror.example.com", "https", "mirror.example.com"},
		{"mirror.example.com", "", "mirror.example.com"},
		{"mirror:5000", "", "mirror:5000"},
	}
	for _, tc := range cases {
		scheme, host := splitMirrorURL(tc.input)
		require.Equal(t, tc.expectedScheme, scheme, "scheme for %s", tc.input)
		require.Equal(t, tc.expectedHost, host, "host for %s", tc.input)
	}
}

func TestSelectMirrorHost_NoConfig(t *testing.T) {
	host, scheme := selectMirrorHost("", "registry.docker.io")
	require.Equal(t, "registry.docker.io", host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	host, scheme := selectMirrorHost(tmpDir, "registry.docker.io")
	require.Equal(t, "registry.docker.io", host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_MirrorNoPingURL(t *testing.T) {
	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, "registry.docker.io", `
[host]
  [host."http://mirror1:5000"]
`)
	host, scheme := selectMirrorHost(tmpDir, "registry.docker.io")
	require.Equal(t, "mirror1:5000", host)
	require.Equal(t, "http", scheme)
}

func TestSelectMirrorHost_MirrorPingSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, "registry.docker.io", `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
`)
	host, scheme := selectMirrorHost(tmpDir, "registry.docker.io")
	require.Equal(t, "mirror1:5000", host)
	require.Equal(t, "http", scheme)
}

func TestSelectMirrorHost_MirrorPingFails_FallbackToOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, "registry.docker.io", `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
`)
	host, scheme := selectMirrorHost(tmpDir, "registry.docker.io")
	require.Equal(t, "registry.docker.io", host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_FirstMirrorFails_SecondMirrorNoPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, "registry.docker.io", `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
  [host."https://mirror2.example.com"]
`)
	host, scheme := selectMirrorHost(tmpDir, "registry.docker.io")
	require.Equal(t, "mirror2.example.com", host)
	require.Equal(t, "https", scheme)
}
