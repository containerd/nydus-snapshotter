/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	buf := []byte(`{
  "device": {
    "backend": {
      "type": "registry",
      "config": {
        "skip_verify": true,
        "host": "acr-nydus-registry-vpc.cn-hangzhou.cr.aliyuncs.com",
        "repo": "test/myserver",
        "auth": "",
        "blob_url_scheme": "http",
        "proxy": {
          "url": "http://p2p-proxy:65001",
          "fallback": true,
          "ping_url": "http://p2p-proxy:40901/server/ping",
          "check_interval": 5
        },
        "timeout": 5,
        "connect_timeout": 5,
        "retry_limit": 0
      }
    },
    "cache": {
      "type": "blobcache",
      "config": {
        "work_dir": "/cache"
      }
    }
  },
  "mode": "direct",
  "digest_validate": true,
  "iostats_files": true,
  "enable_xattr": true,
  "amplify_io": 1048576,
  "fs_prefetch": {
    "enable": true,
    "threads_count": 10,
    "merging_size": 131072,
    "stream_prefetch": true
  }
}`)
	var cfg FuseDaemonConfig
	err := json.Unmarshal(buf, &cfg)
	require.Nil(t, err)
	require.Equal(t, cfg.Enable, true)
	require.Equal(t, cfg.MergingSize, 131072)
	require.Equal(t, cfg.ThreadsCount, 10)
	require.Equal(t, cfg.StreamPrefetch, true)
	require.Equal(t, cfg.Device.Backend.Config.BlobURLScheme, "http")
	require.Equal(t, cfg.Device.Backend.Config.SkipVerify, true)
	require.Equal(t, cfg.Device.Backend.Config.Proxy.CheckInterval, 5)
}

func TestFuseDaemonConfigOptionalFields(t *testing.T) {
	t.Run("amplify_io non-zero", func(t *testing.T) {
		input := []byte(`{"amplify_io": 1048576}`)
		var cfg FuseDaemonConfig
		err := json.Unmarshal(input, &cfg)
		require.Nil(t, err)
		require.Equal(t, *cfg.AmplifyIo, 1048576)
		output, _ := json.Marshal(cfg)
		require.Contains(t, string(output), `"amplify_io":1048576`)
	})

	t.Run("amplify_io zero", func(t *testing.T) {
		input := []byte(`{"amplify_io": 0}`)
		var cfg FuseDaemonConfig
		err := json.Unmarshal(input, &cfg)
		require.Nil(t, err)
		require.Equal(t, *cfg.AmplifyIo, 0)
		output, _ := json.Marshal(cfg)
		require.Contains(t, string(output), `"amplify_io":0`)
	})

	t.Run("amplify_io nil", func(t *testing.T) {
		input := []byte(`{}`)
		var cfg FuseDaemonConfig
		err := json.Unmarshal(input, &cfg)
		require.Nil(t, err)
		require.Nil(t, cfg.AmplifyIo)
		output, _ := json.Marshal(cfg)
		require.NotContains(t, string(output), `amplify_io`)
	})

	t.Run("id_mapping round-trip", func(t *testing.T) {
		// Must serialize as a 3-element JSON array matching nydusd's
		// `RafsConfigV2.id_mapping` (u32, u32, u32) tuple.
		cfg := FuseDaemonConfig{Mode: "direct"}
		cfg.SetIDMapping(&[3]uint32{0, 100000, 65536})

		b, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.Contains(t, string(b), `"id_mapping":[0,100000,65536]`)

		var restored FuseDaemonConfig
		err = json.Unmarshal(b, &restored)
		require.NoError(t, err)
		require.NotNil(t, restored.IDMapping)
		require.Equal(t, [3]uint32{0, 100000, 65536}, *restored.IDMapping)
	})

	t.Run("id_mapping unset omitted", func(t *testing.T) {
		cfg := FuseDaemonConfig{Mode: "direct"}
		b, err := json.Marshal(cfg)
		require.NoError(t, err)
		require.NotContains(t, string(b), "id_mapping")
	})
}

func TestSerializeWithSecretFilter(t *testing.T) {
	buf := []byte(`{
  "device": {
    "backend": {
      "type": "registry",
      "config": {
        "skip_verify": true,
        "host": "acr-nydus-registry-vpc.cn-hangzhou.cr.aliyuncs.com",
        "repo": "test/myserver",
        "auth": "token_token",
        "blob_url_scheme": "http",
        "proxy": {
          "url": "http://p2p-proxy:65001",
          "fallback": true,
          "ping_url": "http://p2p-proxy:40901/server/ping",
          "check_interval": 5
        },
        "timeout": 5,
        "connect_timeout": 5,
        "retry_limit": 0
      }
    },
    "cache": {
      "type": "blobcache",
      "config": {
        "work_dir": "/cache"
      }
    }
  },
  "mode": "direct",
  "digest_validate": true,
  "iostats_files": true,
  "enable_xattr": true,
  "amplify_io": 1048576,
  "fs_prefetch": {
    "enable": true,
    "threads_count": 10,
    "merging_size": 131072
  }
}`)
	var cfg FuseDaemonConfig
	_ = json.Unmarshal(buf, &cfg)
	filter := serializeWithSecretFilter(&cfg)
	jsonData, err := json.Marshal(filter)
	require.Nil(t, err)
	var newCfg FuseDaemonConfig
	err = json.Unmarshal(jsonData, &newCfg)
	require.Nil(t, err)
	require.Equal(t, newCfg.FSPrefetch, cfg.FSPrefetch)
	require.Equal(t, newCfg.Device.Cache.CacheType, cfg.Device.Cache.CacheType)
	require.Equal(t, newCfg.Device.Cache.Config, cfg.Device.Cache.Config)
	require.Equal(t, newCfg.Mode, cfg.Mode)
	require.Equal(t, newCfg.DigestValidate, cfg.DigestValidate)
	require.Equal(t, newCfg.IOStatsFiles, cfg.IOStatsFiles)
	require.Equal(t, newCfg.Device.Backend.Config.Host, cfg.Device.Backend.Config.Host)
	require.Equal(t, newCfg.Device.Backend.Config.Repo, cfg.Device.Backend.Config.Repo)
	require.Equal(t, newCfg.Device.Backend.Config.Proxy, cfg.Device.Backend.Config.Proxy)
	require.Equal(t, newCfg.Device.Backend.Config.BlobURLScheme, cfg.Device.Backend.Config.BlobURLScheme)
	require.Equal(t, newCfg.Device.Backend.Config.Auth, "")
	require.NotEqual(t, newCfg.Device.Backend.Config.Auth, cfg.Device.Backend.Config.Auth)
	require.NotNil(t, newCfg.AmplifyIo)
	require.Equal(t, *newCfg.AmplifyIo, *cfg.AmplifyIo)
}
