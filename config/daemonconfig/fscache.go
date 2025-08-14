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

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/erofs"

	"github.com/pkg/errors"
)

const (
	WorkDir   string = "workdir"
	Bootstrap string = "bootstrap"
)

type BlobPrefetchConfig struct {
	Enable        bool `json:"enable"`
	ThreadsCount  int  `json:"threads_count"`
	MergingSize   int  `json:"merging_size"`
	BandwidthRate int  `json:"bandwidth_rate"`
}

type FscacheDaemonConfig struct {
	// These fields is only for fscache daemon.
	Type string `json:"type"`
	// Snapshotter fills
	ID       string `json:"id"`
	DomainID string `json:"domain_id"`
	Config   *struct {
		ID            string        `json:"id"`
		BackendType   string        `json:"backend_type"`
		BackendConfig BackendConfig `json:"backend_config"`
		CacheType     string        `json:"cache_type"`
		// Snapshotter fills
		CacheConfig struct {
			WorkDir string `json:"work_dir"`
		} `json:"cache_config"`
		BlobPrefetchConfig BlobPrefetchConfig `json:"prefetch_config"`
		MetadataPath       string             `json:"metadata_path"`
	} `json:"config"`
}

// Load Fscache configuration template file
func LoadFscacheConfig(p string) (*FscacheDaemonConfig, error) {
	var cfg FscacheDaemonConfig
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.Wrapf(err, "read fscache configuration file %s", p)
	}
	if err = json.Unmarshal(b, &cfg); err != nil {
		return nil, errors.Wrapf(err, "unmarshal")
	}

	if cfg.Config == nil {
		return nil, errors.New("invalid fscache configuration")
	}

	return &cfg, nil
}

func (c *FscacheDaemonConfig) UpdateMirrors(mirrorsConfigDir, registryHost string) error {
	mirrors, err := LoadMirrorsConfig(mirrorsConfigDir, registryHost)
	if err != nil {
		return err
	}
	if len(mirrors) > 0 {
		c.Config.BackendConfig.Mirrors = mirrors
	}
	return nil
}

func (c *FscacheDaemonConfig) StorageBackend() (string, *BackendConfig) {
	return c.Config.BackendType, &c.Config.BackendConfig
}

// Each fscache/erofs has a configuration with different fscache ID built from snapshot ID.
func (c *FscacheDaemonConfig) Supplement(host, repo, snapshotID string, params map[string]string) {
	if host != "" {
		c.Config.BackendConfig.Host = host
	}
	if repo != "" {
		c.Config.BackendConfig.Repo = repo
	}

	fscacheID := erofs.FscacheID(snapshotID)
	c.ID = fscacheID

	if c.DomainID != "" {
		log.L.Warnf("Linux Kernel Shared Domain feature in use. make sure your kernel version >= 6.1")
	} else {
		c.DomainID = fscacheID
	}

	c.Config.ID = fscacheID

	if WorkDir, ok := params[WorkDir]; ok {
		c.Config.CacheConfig.WorkDir = WorkDir
	}

	if bootstrap, ok := params[Bootstrap]; ok {
		c.Config.MetadataPath = bootstrap
	}
}

func (c *FscacheDaemonConfig) FillAuth(kc *auth.PassKeyChain) {
	if kc != nil {
		if kc.TokenBase() {
			c.Config.BackendConfig.RegistryToken = kc.Password
		} else {
			c.Config.BackendConfig.Auth = kc.ToBase64()
		}
	}
}

func (c *FscacheDaemonConfig) DumpString() (string, error) {
	return DumpConfigString(c)
}

func (c *FscacheDaemonConfig) DumpFile(f string) error {
	if err := os.MkdirAll(path.Dir(f), 0755); err != nil {
		return err
	}

	return DumpConfigFile(c, f)
}
