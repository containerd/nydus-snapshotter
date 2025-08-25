/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"encoding/json"
	"os"
	"path"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
)

const CacheDir string = "cachedir"

// Used when nydusd works as a FUSE daemon or vhost-user-fs backend
type FuseDaemonConfig struct {
	Device          *DeviceConfig `json:"device"`
	Mode            string        `json:"mode"`
	DigestValidate  bool          `json:"digest_validate"`
	IOStatsFiles    bool          `json:"iostats_files,omitempty"`
	EnableXattr     bool          `json:"enable_xattr,omitempty"`
	AccessPattern   bool          `json:"access_pattern,omitempty"`
	LatestReadFiles bool          `json:"latest_read_files,omitempty"`
	AmplifyIo       *int          `json:"amplify_io,omitempty"`
	FSPrefetch      `json:"fs_prefetch,omitempty"`
	// (experimental) The nydus daemon could cache more data to increase hit ratio when enabled the warmup feature.
	Warmup uint64 `json:"warmup,omitempty"`
}

// Control how to perform prefetch from file system layer
type FSPrefetch struct {
	Enable        bool `json:"enable"`
	PrefetchAll   bool `json:"prefetch_all"`
	ThreadsCount  int  `json:"threads_count"`
	MergingSize   int  `json:"merging_size"`
	BandwidthRate int  `json:"bandwidth_rate"`
}

// Load fuse daemon configuration from template file
func LoadFuseConfig(p string) (*FuseDaemonConfig, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.Wrapf(err, "read FUSE configuration file %s", p)
	}
	var cfg FuseDaemonConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, errors.Wrapf(err, "unmarshal %s", p)
	}

	if cfg.Device == nil {
		return nil, errors.New("invalid fuse daemon configuration")
	}

	return &cfg, nil
}

func (c *FuseDaemonConfig) Supplement(host, repo, _ string, params map[string]string) {
	if host != "" {
		c.Device.Backend.Config.Host = host
	}
	if repo != "" {
		c.Device.Backend.Config.Repo = repo
	}
	c.Device.Cache.Config.WorkDir = params[CacheDir]
}

func (c *FuseDaemonConfig) FillAuth(kc *auth.PassKeyChain) {
	if kc != nil {
		if kc.TokenBase() {
			c.Device.Backend.Config.RegistryToken = kc.Password
		} else {
			c.Device.Backend.Config.Auth = kc.ToBase64()
		}
	}
}

func (c *FuseDaemonConfig) UpdateMirrors(mirrorsConfigDir, registryHost string) error {
	mirrors, err := LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	if err != nil {
		return err
	}
	if len(mirrors) > 0 {
		c.Device.Backend.Config.Mirrors = mirrors
	}
	return nil
}

func (c *FuseDaemonConfig) StorageBackend() (string, *BackendConfig) {
	return c.Device.Backend.BackendType, &c.Device.Backend.Config
}

func (c *FuseDaemonConfig) DumpString() (string, error) {
	return DumpConfigString(c)
}

func (c *FuseDaemonConfig) DumpFile(f string) error {
	if err := os.MkdirAll(path.Dir(f), 0755); err != nil {
		return err
	}
	return DumpConfigFile(c, f)
}
