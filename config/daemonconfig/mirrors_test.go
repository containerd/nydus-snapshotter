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
	require.Nil(t, mirrors)

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
