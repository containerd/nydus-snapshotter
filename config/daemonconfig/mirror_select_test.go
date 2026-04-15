/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
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

var testRegistryHost = "fake-test.registry.com"

func writeMirrorHostsToml(t *testing.T, dir, content string) {
	t.Helper()
	hostDir := filepath.Join(dir, testRegistryHost)
	require.NoError(t, os.MkdirAll(hostDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(content), 0600))
}

func TestSplitMirrorURL(t *testing.T) {
	cases := []struct {
		name           string
		input          string
		expectedScheme string
		expectedHost   string
		expectErr      bool
	}{
		{
			name:           "http with port",
			input:          "http://mirror:5000",
			expectedScheme: "http",
			expectedHost:   "mirror:5000",
		},
		{
			name:           "https without port",
			input:          "https://mirror.example.com",
			expectedScheme: "https",
			expectedHost:   "mirror.example.com",
		},
		{
			name:           "no scheme, host only",
			input:          "mirror.example.com",
			expectedScheme: "https",
			expectedHost:   "mirror.example.com",
		},
		{
			name:           "no scheme, host with port",
			input:          "mirror:5000",
			expectedScheme: "https",
			expectedHost:   "mirror:5000",
		},
		{
			name:           "https with port",
			input:          "https://mirror.example.com:5000",
			expectedScheme: "https",
			expectedHost:   "mirror.example.com:5000",
		},
		{
			name:           "http with path",
			input:          "http://mirror.example.com/v2",
			expectedScheme: "http",
			expectedHost:   "mirror.example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme, host, err := splitMirrorURL(tc.input)
			if tc.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.expectedScheme, scheme, "scheme for %s", tc.input)
			require.Equal(t, tc.expectedHost, host, "host for %s", tc.input)
		})
	}
}

func TestSelectMirrorHost_NoConfig(t *testing.T) {
	scheme, host := selectMirrorHost("", testRegistryHost)
	require.Equal(t, testRegistryHost, host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	scheme, host := selectMirrorHost(tmpDir, testRegistryHost)
	require.Equal(t, testRegistryHost, host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_MirrorNoPingURL(t *testing.T) {
	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, `
[host]
  [host."http://mirror1:5000"]
`)
	scheme, host := selectMirrorHost(tmpDir, testRegistryHost)
	require.Equal(t, "mirror1:5000", host)
	require.Equal(t, "http", scheme)
}

func TestSelectMirrorHost_MirrorPingSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
`)
	scheme, host := selectMirrorHost(tmpDir, testRegistryHost)
	require.Equal(t, "mirror1:5000", host)
	require.Equal(t, "http", scheme)
}

func TestSelectMirrorHost_MirrorPingFails_FallbackToOrigin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
`)
	scheme, host := selectMirrorHost(tmpDir, testRegistryHost)
	require.Equal(t, testRegistryHost, host)
	require.Equal(t, "", scheme)
}

func TestSelectMirrorHost_FirstMirrorFails_SecondMirrorNoPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	writeMirrorHostsToml(t, tmpDir, `
[host]
  [host."http://mirror1:5000"]
    ping_url = "`+srv.URL+`"
  [host."https://mirror2.example.com"]
`)
	scheme, host := selectMirrorHost(tmpDir, testRegistryHost)
	require.Equal(t, "mirror2.example.com", host)
	require.Equal(t, "https", scheme)
}
