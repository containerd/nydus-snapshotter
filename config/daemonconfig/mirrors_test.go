/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMirrorConfig(t *testing.T) {
	tmpDir := t.TempDir()
	defer os.RemoveAll(tmpDir)

	registryHost := "registry.docker.io"

	mirrorsConfigDir := filepath.Join(tmpDir, "certs.d")
	registryHostConfigDir := filepath.Join(mirrorsConfigDir, registryHost)
	defaultHostConfigDir := filepath.Join(mirrorsConfigDir, "_default")

	mirrors, err := LoadMirrorsConfig("", registryHost)
	require.NoError(t, err)
	require.Nil(t, mirrors)

	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Nil(t, mirrors)

	err = os.MkdirAll(defaultHostConfigDir, os.ModePerm)
	assert.NoError(t, err)

	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
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
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
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
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
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
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	require.NoError(t, err)
	require.Equal(t, len(mirrors), 1)
	require.Equal(t, mirrors[0].Host, "http://p2p-mirror2:65001")
	require.Equal(t, mirrors[0].Headers["X-Dragonfly-Registry"], "https://docker.hub.com")
}
