/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/utils/file"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
)

// Copied from containerd, for compatibility with containerd's toml configuration file.
type hostFileConfig struct {
	Capabilities []string               `toml:"capabilities"`
	CACert       interface{}            `toml:"ca"`
	Client       interface{}            `toml:"client"`
	SkipVerify   *bool                  `toml:"skip_verify"`
	Header       map[string]interface{} `toml:"header"`
	OverridePath bool                   `toml:"override_path"`

	// The following configuration items are specific to nydus.
	AuthThrough         bool   `toml:"auth_through,omitempty"`
	HealthCheckInterval int    `toml:"health_check_interval,omitempty"`
	FailureLimit        uint8  `toml:"failure_limit,omitempty"`
	PingURL             string `toml:"ping_url,omitempty"`
}

func makeStringSlice(slice []interface{}, cb func(string) string) ([]string, error) {
	out := make([]string, len(slice))
	for i, value := range slice {
		str, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("unable to cast %v to string", value)
		}

		if cb != nil {
			out[i] = cb(str)
		} else {
			out[i] = str
		}
	}
	return out, nil
}

func parseMirrorsConfigFromToml(b []byte) ([]MirrorConfig, error) {
	var parsedMirrors []MirrorConfig

	c := struct {
		HostConfigs map[string]hostFileConfig `toml:"host"`
	}{}
	tree, err := toml.LoadBytes(b)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}
	if err := tree.Unmarshal(&c); err != nil {
		return nil, err
	}

	for key, value := range c.HostConfigs {
		mirror := MirrorConfig{
			Host:                key,
			AuthThrough:         value.AuthThrough,
			HealthCheckInterval: value.HealthCheckInterval,
			FailureLimit:        value.FailureLimit,
			PingURL:             value.PingURL,
		}
		if value.Header != nil {
			header := http.Header{}
			mirrorHeader := make(map[string]string, 0)
			for key, ty := range value.Header {
				switch v := ty.(type) {
				case string:
					header[key] = []string{v}
				case []interface{}:
					header[key], err = makeStringSlice(v, nil)
					if err != nil {
						return nil, err
					}
				default:
					return nil, fmt.Errorf("invalid type %v for header %q", ty, key)
				}
				mirrorHeader[key] = header.Get(key)
				if len(header[key]) > 1 {
					log.L.Warnf("some values of the header[%q] are omitted: %#v", key, header.Values(key)[1:])
				}
			}
			mirror.Headers = mirrorHeader

		}
		parsedMirrors = append(parsedMirrors, mirror)
	}

	return parsedMirrors, nil
}

func parseMirrorsConfig(path string, b []byte) ([]MirrorConfig, error) {
	format := strings.Trim(filepath.Ext(path), ".")

	var parsedMirrors []MirrorConfig
	var err error
	switch format {
	case "toml":
		parsedMirrors, err = parseMirrorsConfigFromToml(b)
	default:
		return nil, errors.Errorf("invalid file path: %v, supported suffix includes [ \"toml\" ]", path)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "invalid config file %s", path)
	}
	return parsedMirrors, nil
}

func LoadMirrorsConfig(mirrorsConfigDir string) ([]MirrorConfig, error) {
	var mirrors []MirrorConfig

	if mirrorsConfigDir == "" {
		return mirrors, nil
	}
	dirExisted, err := file.IsDirExisted(mirrorsConfigDir)
	if err != nil {
		return nil, err
	}
	if !dirExisted {
		return nil, errors.Errorf("mirrors config directory %s is not existed", mirrorsConfigDir)
	}

	if err := filepath.Walk(mirrorsConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return errors.Errorf("read mirror configuration file %s failed: %v", path, err)
		}

		parsedMirrors, err := parseMirrorsConfig(path, b)
		if err != nil {
			return errors.Errorf("parse mirrors config failed: %v", err)
		}
		mirrors = append(mirrors, parsedMirrors...)

		return nil
	}); err != nil {
		return nil, err
	}

	return mirrors, nil
}
