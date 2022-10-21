/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"encoding/json"
	"os"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/erofs"

	"github.com/pkg/errors"
)

const (
	WorkDir   string = "workdir"
	Bootstrap string = "bootstrap"
)

type FscacheDaemonConfig struct {
	// These fields is only for fscache daemon.
	Type string `json:"type"`
	// Snapshotter fills
	ID string `json:"id"`
	// Snapshotter fills
	DomainID string `json:"domain_id"`
	Config   struct {
		ID            string        `json:"id"`
		BackendType   string        `json:"backend_type"`
		BackendConfig BackendConfig `json:"backend_config"`
		CacheType     string        `json:"cache_type"`
		// Snapshotter fills
		CacheConfig struct {
			WorkDir string `json:"work_dir"`
		} `json:"cache_config"`
		MetadataPath string `json:"metadata_path"`
	} `json:"config"`
	FSPrefetch `json:"fs_prefetch,omitempty"`
}

// Load Fscache configuration template file
func LoadFscacheConfig(p string) (cfg *FscacheDaemonConfig, err error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, errors.Wrapf(err, "read fscache configuration file")
	}
	if err = json.Unmarshal(b, cfg); err != nil {
		return nil, errors.Wrapf(err, "unmarshal")
	}
	return
}

func (c *FscacheDaemonConfig) StorageBackendType() string {
	return c.Config.BackendType
}

func (c *FscacheDaemonConfig) Supplement(host, repo, snapshotID string, params map[string]string) {
	c.Config.BackendConfig.Host = host
	c.Config.BackendConfig.Repo = repo

	fscacheID := erofs.FscacheID(snapshotID)
	c.ID = fscacheID
	c.DomainID = fscacheID
	c.Config.ID = fscacheID
	c.Config.CacheConfig.WorkDir = params[WorkDir]
	c.Config.MetadataPath = params[Bootstrap]
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

func (c *FscacheDaemonConfig) DumpFile(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return errdefs.ErrAlreadyExists
	}
	return DumpConfigFile(c, path)
}
