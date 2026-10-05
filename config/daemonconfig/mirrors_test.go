/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMirrorConfigCACerts(t *testing.T) {
	registryHost := "registry.example.com"

	t.Run("single CA cert as absolute path", func(t *testing.T) {
		tmpDir := t.TempDir()
		hostDir := filepath.Join(tmpDir, "certs.d", registryHost)
		require.NoError(t, os.MkdirAll(hostDir, os.ModePerm))

		caPath := filepath.Join(tmpDir, "my-ca.pem")
		require.NoError(t, os.WriteFile(caPath, []byte(""), 0600))

		hosts := fmt.Sprintf(`
[host."https://mirror.example.com"]
  ca = %q
`, caPath)
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hosts), 0600))

		_, caCerts, err := LoadMirrorsConfig(filepath.Join(tmpDir, "certs.d"), registryHost)
		require.NoError(t, err)
		require.Equal(t, []string{caPath}, caCerts)
	})

	t.Run("multiple CA certs as array", func(t *testing.T) {
		tmpDir := t.TempDir()
		hostDir := filepath.Join(tmpDir, "certs.d", registryHost)
		require.NoError(t, os.MkdirAll(hostDir, os.ModePerm))

		ca1 := filepath.Join(tmpDir, "ca1.pem")
		ca2 := filepath.Join(tmpDir, "ca2.pem")

		hosts := fmt.Sprintf(`
[host."https://mirror.example.com"]
  ca = [%q, %q]
`, ca1, ca2)
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hosts), 0600))

		_, caCerts, err := LoadMirrorsConfig(filepath.Join(tmpDir, "certs.d"), registryHost)
		require.NoError(t, err)
		require.Equal(t, []string{ca1, ca2}, caCerts)
	})

	t.Run("CA certs deduplicated across multiple hosts", func(t *testing.T) {
		tmpDir := t.TempDir()
		hostDir := filepath.Join(tmpDir, "certs.d", registryHost)
		require.NoError(t, os.MkdirAll(hostDir, os.ModePerm))

		ca1 := filepath.Join(tmpDir, "ca1.pem")
		ca2 := filepath.Join(tmpDir, "ca2.pem")

		hosts := fmt.Sprintf(`
[host."https://mirror1.example.com"]
  ca = %q

[host."https://mirror2.example.com"]
  ca = [%q, %q]
`, ca1, ca1, ca2)
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hosts), 0600))

		_, caCerts, err := LoadMirrorsConfig(filepath.Join(tmpDir, "certs.d"), registryHost)
		require.NoError(t, err)
		require.Equal(t, []string{ca1, ca2}, caCerts)
	})

	t.Run("no CA cert field returns nil caCerts", func(t *testing.T) {
		tmpDir := t.TempDir()
		hostDir := filepath.Join(tmpDir, "certs.d", registryHost)
		require.NoError(t, os.MkdirAll(hostDir, os.ModePerm))

		hosts := `
[host."https://mirror.example.com"]
`
		require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hosts), 0600))

		_, caCerts, err := LoadMirrorsConfig(filepath.Join(tmpDir, "certs.d"), registryHost)
		require.NoError(t, err)
		require.Nil(t, caCerts)
	})
}

func TestLoadMirrorConfig(t *testing.T) {
	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)

	registryHost := "registry.docker.io"

	mirrorsConfigDir := filepath.Join(tmpDir, "certs.d")
	registryHostConfigDir := filepath.Join(mirrorsConfigDir, registryHost)
	defaultHostConfigDir := filepath.Join(mirrorsConfigDir, "_default")

	mirrors, _, err := LoadMirrorsConfig("", registryHost)
	require.NoError(t, err)
	require.Nil(t, mirrors)

	mirrors, _, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Nil(t, mirrors)

	err = os.MkdirAll(defaultHostConfigDir, os.ModePerm)
	assert.NoError(t, err)

	mirrors, _, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Equal(t, len(mirrors), 0)

	buf1 := []byte(`server = "https://default-docker.hub.com"
	[host]
	  [host."http://default-p2p-mirror1:65001"]
		[host."http://default-p2p-mirror1:65001".header]
		  X-Dragonfly-Registry = ["https://default-docker.hub.com"]
	`)
	err = os.WriteFile(filepath.Join(defaultHostConfigDir, "hosts.toml"), buf1, 0600)
	assert.NoError(t, err)
	mirrors, _, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Equal(t, len(mirrors), 1)
	require.Equal(t, mirrors[0].Host, "http://default-p2p-mirror1:65001")
	require.Equal(t, mirrors[0].Headers["X-Dragonfly-Registry"], "https://default-docker.hub.com")

	err = os.MkdirAll(registryHostConfigDir, os.ModePerm)
	assert.NoError(t, err)

	buf2 := []byte(`server = "https://docker.hub.com"
	[host]
	  [host."http://p2p-mirror1:65001"]
		[host."http://p2p-mirror1:65001".header]
		  X-Dragonfly-Registry = ["https://docker.hub.com"]
	`)
	err = os.WriteFile(filepath.Join(registryHostConfigDir, "hosts.toml"), buf2, 0600)
	assert.NoError(t, err)
	mirrors, _, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Equal(t, len(mirrors), 1)
	require.Equal(t, mirrors[0].Host, "http://p2p-mirror1:65001")
	require.Equal(t, mirrors[0].Headers["X-Dragonfly-Registry"], "https://docker.hub.com")

	buf3 := []byte(`
		[host."http://p2p-mirror2:65001"]
		[host."http://p2p-mirror2:65001".header]
			X-Dragonfly-Registry = ["https://docker.hub.com"]
	`)
	err = os.WriteFile(filepath.Join(registryHostConfigDir, "hosts.toml"), buf3, 0600)
	assert.NoError(t, err)
	mirrors, _, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Equal(t, len(mirrors), 1)
	require.Equal(t, mirrors[0].Host, "http://p2p-mirror2:65001")
	require.Equal(t, mirrors[0].Headers["X-Dragonfly-Registry"], "https://docker.hub.com")
}

func TestParseHostConfigPath(t *testing.T) {
	cases := []struct {
		name         string
		server       string
		overridePath bool
		expectedPath string
	}{
		{name: "no path", server: "http://mirror:5000", expectedPath: "/v2"},
		{name: "no scheme", server: "mirror:5000", expectedPath: "/v2"},
		{name: "root path", server: "https://mirror/", expectedPath: "/v2"},
		{name: "v2 path", server: "https://mirror/v2", expectedPath: "/v2"},
		{name: "v2 path with override_path", server: "https://mirror/v2", overridePath: true, expectedPath: "/v2"},
		{name: "v2 prefix with override_path", server: "http://harbor/v2/proxy", overridePath: true, expectedPath: "/v2/proxy"},
		{name: "trailing slash with override_path", server: "http://harbor/v2/proxy/", overridePath: true, expectedPath: "/v2/proxy"},
		{name: "nested prefix with override_path", server: "http://harbor/v2/a/b", overridePath: true, expectedPath: "/v2/a/b"},
		{name: "v2 prefix without override_path", server: "http://harbor/v2/proxy", expectedPath: "/v2/proxy/v2"},
		{name: "other path without override_path", server: "http://mirror/registry", expectedPath: "/registry/v2"},
		{name: "other path with override_path", server: "http://mirror/api/registry", overridePath: true, expectedPath: "/api/registry"},
		{name: "override_path without path", server: "http://mirror", overridePath: true, expectedPath: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := parseHostConfig(tc.server, HostFileConfig{OverridePath: tc.overridePath})
			require.NoError(t, err)
			require.Equal(t, tc.expectedPath, h.Path)
		})
	}
}

func TestRepoPrefixFromAPIPath(t *testing.T) {
	cases := []struct {
		apiPath        string
		expectedPrefix string
		expectedOK     bool
	}{
		{apiPath: "/v2", expectedOK: true},
		{apiPath: "/v2/proxy", expectedPrefix: "proxy", expectedOK: true},
		{apiPath: "/v2/a/b", expectedPrefix: "a/b", expectedOK: true},
		{apiPath: "/v2/proxy/v2", expectedPrefix: "proxy/v2", expectedOK: true},
		{apiPath: "/registry/v2", expectedOK: false},
		{apiPath: "/v2proxy", expectedOK: false},
		{apiPath: "", expectedOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.apiPath, func(t *testing.T) {
			prefix, ok := repoPrefixFromAPIPath(tc.apiPath)
			require.Equal(t, tc.expectedOK, ok)
			require.Equal(t, tc.expectedPrefix, prefix)
		})
	}
}

func TestLoadMirrorsConfigRepoPrefix(t *testing.T) {
	registryHost := "registry.example.com"
	tmpDir := t.TempDir()
	hostDir := filepath.Join(tmpDir, registryHost)
	require.NoError(t, os.MkdirAll(hostDir, os.ModePerm))

	hosts := `
server = "https://registry.example.com"

[host."http://harbor.example.com/v2/proxy-project"]
  capabilities = ["pull", "resolve"]
  override_path = true

[host."http://mirror.example.com/v2/proxy-project"]

[host."http://other.example.com/api/registry"]
  override_path = true

[host."https://registry.example.com"]
  capabilities = ["pull", "resolve"]
`
	require.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(hosts), 0600))

	mirrors, _, err := LoadMirrorsConfig(tmpDir, registryHost)
	require.NoError(t, err)
	require.Len(t, mirrors, 4)
	require.Equal(t, "http://harbor.example.com", mirrors[0].Host)
	require.Equal(t, "proxy-project", mirrors[0].RepoPrefix)
	// Without override_path the path is ignored, as before.
	require.Equal(t, "http://mirror.example.com", mirrors[1].Host)
	require.Equal(t, "", mirrors[1].RepoPrefix)
	// Not expressible as "/v2/<repo>": the path is ignored.
	require.Equal(t, "http://other.example.com", mirrors[2].Host)
	require.Equal(t, "", mirrors[2].RepoPrefix)
	require.Equal(t, "https://registry.example.com", mirrors[3].Host)
	require.Equal(t, "", mirrors[3].RepoPrefix)
}
