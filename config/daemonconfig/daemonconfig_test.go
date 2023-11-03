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
    "merging_size": 131072
  }
}`)
	var cfg FuseDaemonConfig
	err := json.Unmarshal(buf, &cfg)
	require.Nil(t, err)
	require.Equal(t, cfg.FSPrefetch.Enable, true)
	require.Equal(t, cfg.FSPrefetch.MergingSize, 131072)
	require.Equal(t, cfg.FSPrefetch.ThreadsCount, 10)
	require.Equal(t, cfg.Device.Backend.Config.BlobURLScheme, "http")
	require.Equal(t, cfg.Device.Backend.Config.SkipVerify, true)
	require.Equal(t, cfg.Device.Backend.Config.Proxy.CheckInterval, 5)
}

func TestAmplifyIo(t *testing.T) {
	// Test non-zero value
	input1 := []byte(`{"amplify_io": 1048576}`)
	var cfg1 FuseDaemonConfig
	err1 := json.Unmarshal(input1, &cfg1)
	require.Nil(t, err1)
	require.Equal(t, *cfg1.AmplifyIo, 1048576)
	output1, _ := json.Marshal(cfg1)
	require.Contains(t, string(output1), `"amplify_io":1048576`)

	// Test zero value
	input2 := []byte(`{"amplify_io": 0}`)
	var cfg2 FuseDaemonConfig
	err2 := json.Unmarshal(input2, &cfg2)
	require.Nil(t, err2)
	require.Equal(t, *cfg2.AmplifyIo, 0)
	output2, _ := json.Marshal(cfg2)
	require.Contains(t, string(output2), `"amplify_io":0`)

	// Test nil value
	input3 := []byte(`{}`)
	var cfg3 FuseDaemonConfig
	err3 := json.Unmarshal(input3, &cfg3)
	require.Nil(t, err3)
	require.Nil(t, cfg3.AmplifyIo)
	output3, _ := json.Marshal(cfg3)
	require.NotContains(t, string(output3), `amplify_io`)
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
}
