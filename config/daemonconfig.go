/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package config

import (
	"encoding/json"
	"io/ioutil"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/erofs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

const (
	backendTypeLocalfs  = "localfs"
	backendTypeOss      = "oss"
	backendTypeRegistry = "registry"
)

type ErofsDaemonConfig struct {
	// These fields is only for erofs daemon.
	Type     string `json:"type"`
	ID       string `json:"id"`
	DomainID string `json:"domain_id"`
	Config   struct {
		ID            string        `json:"id"`
		BackendType   string        `json:"backend_type"`
		BackendConfig BackendConfig `json:"backend_config"`
		CacheType     string        `json:"cache_type"`
		CacheConfig   struct {
			WorkDir string `json:"workdir"`
		} `json:"cache_config"`
		MetadataPath string `json:"metadata_path"`
	} `json:"config"`
}

type DaemonConfig struct {
	Device         DeviceConfig `json:"device"`
	Mode           string       `json:"mode"`
	DigestValidate bool         `json:"digest_validate"`
	IOStatsFiles   bool         `json:"iostats_files,omitempty"`
	EnableXattr    bool         `json:"enable_xattr,omitempty"`
	FSPrefetch     struct {
		Enable       bool `json:"enable"`
		PrefetchAll  bool `json:"prefetch_all"`
		ThreadsCount int  `json:"threads_count"`
		MergingSize  int  `json:"merging_size"`
	} `json:"fs_prefetch,omitempty"`

	ErofsDaemonConfig
}

type BackendConfig struct {
	// Localfs backend configs
	BlobFile     string `json:"blob_file,omitempty"`
	Dir          string `json:"dir,omitempty"`
	ReadAhead    bool   `json:"readahead"`
	ReadAheadSec int    `json:"readahead_sec,omitempty"`

	// Registry backend configs
	Host               string `json:"host,omitempty"`
	Repo               string `json:"repo,omitempty"`
	Auth               string `json:"auth,omitempty"`
	RegistryToken      string `json:"registry_token,omitempty"`
	BlobURLScheme      string `json:"blob_url_scheme,omitempty"`
	BlobRedirectedHost string `json:"blob_redirected_host,omitempty"`

	// OSS backend configs
	EndPoint        string `json:"endpoint,omitempty"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	AccessKeySecret string `json:"access_key_secret,omitempty"`
	BucketName      string `json:"bucket_name,omitempty"`
	ObjectPrefix    string `json:"object_prefix,omitempty"`

	// Shared by registry and oss backend
	Scheme string `json:"scheme,omitempty"`

	// Below configs are common configs shared by all backends
	Proxy struct {
		URL           string `json:"url,omitempty"`
		Fallback      bool   `json:"fallback"`
		PingURL       string `json:"ping_url,omitempty"`
		CheckInterval int    `json:"check_interval,omitempty"`
	} `json:"proxy,omitempty"`
	Timeout        int `json:"timeout,omitempty"`
	ConnectTimeout int `json:"connect_timeout,omitempty"`
	RetryLimit     int `json:"retry_limit,omitempty"`
}

type DeviceConfig struct {
	Backend struct {
		BackendType string        `json:"type"`
		Config      BackendConfig `json:"config"`
	} `json:"backend"`
	Cache struct {
		CacheType  string `json:"type"`
		Compressed bool   `json:"compressed,omitempty"`
		Config     struct {
			WorkDir           string `json:"work_dir"`
			DisableIndexedMap bool   `json:"disable_indexed_map"`
		} `json:"config"`
	} `json:"cache"`
}

func LoadConfig(configFile string, cfg *DaemonConfig) error {
	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, cfg); err != nil {
		return err
	}
	return nil
}

func SaveConfig(c interface{}, configFile string) error {
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	return ioutil.WriteFile(configFile, b, 0755)
}

func NewDaemonConfig(daemonBackend string, cfg DaemonConfig, imageID string, vpcRegistry bool, labels map[string]string) (DaemonConfig, error) {
	image, err := registry.ParseImage(imageID)
	if err != nil {
		return DaemonConfig{}, errors.Wrapf(err, "failed to parse image %s", imageID)
	}

	backend := cfg.Device.Backend.BackendType
	if daemonBackend == DaemonBackendErofs {
		backend = cfg.Config.BackendType
	}

	switch backend {
	case backendTypeRegistry:
		registryHost := image.Host
		if vpcRegistry {
			registryHost = registry.ConvertToVPCHost(registryHost)
		} else if registryHost == "docker.io" {
			// For docker.io images, we should use index.docker.io
			registryHost = "index.docker.io"
		}
		keyChain := auth.GetRegistryKeyChain(registryHost, labels)
		// If no auth is provided, don't touch auth from provided nydusd configuration file.
		// We don't validate the original nydusd auth from configuration file since it can be empty
		// when repository is public.
		backendConfig := &cfg.Device.Backend.Config
		if daemonBackend == DaemonBackendErofs {
			backendConfig = &cfg.Config.BackendConfig
			fscacheID := erofs.FscacheID(imageID)
			cfg.ID = fscacheID
			cfg.DomainID = fscacheID
			cfg.Config.ID = fscacheID
		}
		if keyChain != nil {
			if keyChain.TokenBase() {
				backendConfig.RegistryToken = keyChain.Password
			} else {
				backendConfig.Auth = keyChain.ToBase64()
			}
		}
		backendConfig.Host = registryHost
		backendConfig.Repo = image.Repo
	// Localfs and OSS backends don't need any update, just use the provided config in template
	case backendTypeLocalfs:
	case backendTypeOss:
	default:
		return DaemonConfig{}, errors.Errorf("unknown backend type %s", backend)
	}

	return cfg, nil
}
