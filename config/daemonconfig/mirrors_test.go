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

	mirrorsConfigDir := filepath.Join(tmpDir, "certs.d")
	mirrorsInvalidConfigDir := filepath.Join(tmpDir, "certs.d.file")
	mirrorsConfigPath1 := filepath.Join(mirrorsConfigDir, "test1.toml")
	mirrorsConfigPath2 := filepath.Join(mirrorsConfigDir, "test2.toml")
	mirrorsConfigPath3 := filepath.Join(mirrorsConfigDir, "test3.toml")
	mirrorsConfigPath4 := filepath.Join(mirrorsConfigDir, "test4.yaml")
	mirrorsConfigPath5 := filepath.Join(mirrorsConfigDir, "test5.toml")
	mirrorsInvalidConfigPath1 := filepath.Join(mirrorsConfigDir, "dir")

	mirrors, err := LoadMirrorsConfig("")
	require.Nil(t, err)
	require.Nil(t, mirrors)

	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.Error(t, err)
	require.Nil(t, mirrors)

	err = os.Mkdir(mirrorsConfigDir, os.ModePerm)
	assert.NoError(t, err)

	err = os.WriteFile(mirrorsInvalidConfigDir, []byte(`""`), 0600)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsInvalidConfigDir)
	require.ErrorContains(t, err, "is not existed")
	require.Nil(t, mirrors)

	buf1 := []byte(`server = "https://docker.hub.com"

	[host]

	  [host."http://p2p-mirror1:65001"]
		auth_through = false

		[host."http://p2p-mirror1:65001".header]
		  X-Dragonfly-Registry = ["https://docker.hub.com"]`)
	err = os.WriteFile(mirrorsConfigPath1, buf1, 0600)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.Nil(t, err)
	require.Equal(t, len(mirrors), 1)
	require.Equal(t, mirrors[0].Host, "http://p2p-mirror1:65001")
	require.Equal(t, mirrors[0].AuthThrough, false)
	require.Equal(t, mirrors[0].Headers["X-Dragonfly-Registry"], "https://docker.hub.com")

	err = os.Mkdir(mirrorsInvalidConfigPath1, os.ModePerm)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.Nil(t, err)
	require.Equal(t, len(mirrors), 1)

	buf2 := []byte(`server = "https://docker.hub.com"

	[host]

	  [host."http://p2p-mirror2:65001"]
		auth_through = false
	
		[host."http://p2p-mirror2:65001".header]
		  X-Dragonfly-Registry = ["https://docker.hub.com"]`)
	err = os.WriteFile(mirrorsConfigPath2, buf2, 0600)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.Nil(t, err)
	require.Equal(t, len(mirrors), 2)

	buf3 := []byte(`server = "https://docker.hub.com"

	[host]

	  [host."http://127.0.0.1:65001"]
	    capabilities = ["pull", "resolve"]
	    auth_through = true

	    [host."http://127.0.0.1:65001".header]
	      X-Dragonfly-Registry = ["https://docker.hub.com"]`)
	err = os.WriteFile(mirrorsConfigPath3, buf3, 0600)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.Nil(t, err)
	require.Equal(t, len(mirrors), 3)
	require.Equal(t, mirrors[2].Host, "http://127.0.0.1:65001")
	require.Equal(t, mirrors[2].AuthThrough, true)
	require.Equal(t, mirrors[2].Headers["X-Dragonfly-Registry"], "https://docker.hub.com")

	buf4 := []byte(`[test]`)
	err = os.WriteFile(mirrorsConfigPath4, buf4, 0600)
	assert.NoError(t, err)
	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.ErrorContains(t, err, "invalid file path")
	require.Equal(t, len(mirrors), 0)

	buf5 := []byte(`}`)
	err = os.MkdirAll(mirrorsConfigDir, os.ModePerm)
	assert.NoError(t, err)
	err = os.WriteFile(mirrorsConfigPath5, buf5, 0600)
	assert.NoError(t, err)

	mirrors, err = LoadMirrorsConfig(mirrorsConfigDir)
	require.ErrorContains(t, err, "invalid file path")
	require.Equal(t, len(mirrors), 0)
}
