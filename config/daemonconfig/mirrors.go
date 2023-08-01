/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemonconfig

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/containerd/log"
	"github.com/pelletier/go-toml"
	"github.com/pkg/errors"
)

// Copied from containerd, for compatibility with containerd's toml configuration file.
type HostFileConfig struct {
	Capabilities []string               `toml:"capabilities"`
	CACert       interface{}            `toml:"ca"`
	Client       interface{}            `toml:"client"`
	SkipVerify   *bool                  `toml:"skip_verify"`
	Header       map[string]interface{} `toml:"header"`
	OverridePath bool                   `toml:"override_path"`

	// The following configuration items are specific to nydus.
	HealthCheckInterval int    `toml:"health_check_interval,omitempty"`
	FailureLimit        uint8  `toml:"failure_limit,omitempty"`
	PingURL             string `toml:"ping_url,omitempty"`
}

type hostConfig struct {
	Scheme string
	Host   string
	Header http.Header

	HealthCheckInterval int
	FailureLimit        uint8
	PingURL             string
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

func parseMirrorsConfig(hosts []hostConfig) []MirrorConfig {
	var parsedMirrors = make([]MirrorConfig, len(hosts))

	for i, host := range hosts {
		parsedMirrors[i].Host = fmt.Sprintf("%s://%s", host.Scheme, host.Host)
		parsedMirrors[i].HealthCheckInterval = host.HealthCheckInterval
		parsedMirrors[i].FailureLimit = host.FailureLimit
		parsedMirrors[i].PingURL = host.PingURL

		if len(host.Header) > 0 {
			mirrorHeader := make(map[string]string, len(host.Header))
			for key, value := range host.Header {
				if len(value) > 1 {
					log.L.Warnf("some values of the header[%q] are omitted: %#v", key, value[1:])
				}
				mirrorHeader[key] = host.Header.Get(key)
			}
			parsedMirrors[i].Headers = mirrorHeader
		}
	}

	return parsedMirrors
}

// hostDirectory converts ":port" to "_port_" in directory names
func hostDirectory(host string) string {
	idx := strings.LastIndex(host, ":")
	if idx > 0 {
		return host[:idx] + "_" + host[idx+1:] + "_"
	}
	return host
}

func hostPaths(root, host string) []string {
	var hosts []string
	ch := hostDirectory(host)
	if ch != host {
		hosts = append(hosts, filepath.Join(root, ch))
	}
	return append(hosts,
		filepath.Join(root, host),
		filepath.Join(root, "_default"),
	)
}

func hostDirFromRoot(root, host string) (string, error) {
	for _, p := range hostPaths(root, host) {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

// getSortedHosts returns the list of hosts as they defined in the file.
func getSortedHosts(root *toml.Tree) ([]string, error) {
	iter, ok := root.Get("host").(*toml.Tree)
	if !ok {
		return nil, errors.New("invalid `host` tree")
	}

	list := append([]string{}, iter.Keys()...)

	// go-toml stores TOML sections in the map object, so no order guaranteed.
	// We retrieve line number for each key and sort the keys by position.
	sort.Slice(list, func(i, j int) bool {
		h1 := iter.GetPath([]string{list[i]}).(*toml.Tree)
		h2 := iter.GetPath([]string{list[j]}).(*toml.Tree)
		return h1.Position().Line < h2.Position().Line
	})

	return list, nil
}

// parseHostConfig returns the parsed host configuration, make sure the server is not null.
func parseHostConfig(server string, config HostFileConfig) (hostConfig, error) {
	var (
		result = hostConfig{}
		err    error
	)

	if !strings.HasPrefix(server, "http") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil {
		return hostConfig{}, fmt.Errorf("unable to parse server %v: %w", server, err)
	}
	result.Scheme = u.Scheme
	result.Host = u.Host

	if config.Header != nil {
		header := http.Header{}
		for key, ty := range config.Header {
			switch value := ty.(type) {
			case string:
				header[key] = []string{value}
			case []interface{}:
				header[key], err = makeStringSlice(value, nil)
				if err != nil {
					return hostConfig{}, err
				}
			default:
				return hostConfig{}, fmt.Errorf("invalid type %v for header %q", ty, key)
			}
		}
		result.Header = header
	}

	result.HealthCheckInterval = config.HealthCheckInterval
	result.FailureLimit = config.FailureLimit
	result.PingURL = config.PingURL

	return result, nil
}

func parseHostsFile(b []byte) ([]hostConfig, error) {
	tree, err := toml.LoadBytes(b)
	if err != nil {
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}
	c := struct {
		// HostConfigs store the per-host configuration
		HostConfigs map[string]HostFileConfig `toml:"host"`
	}{}

	orderedHosts, err := getSortedHosts(tree)
	if err != nil {
		return nil, err
	}

	var (
		hosts []hostConfig
	)

	if err := tree.Unmarshal(&c); err != nil {
		return nil, err
	}

	// Parse hosts array
	for _, host := range orderedHosts {
		if host != "" {
			config := c.HostConfigs[host]
			parsed, err := parseHostConfig(host, config)
			if err != nil {
				return nil, err
			}
			hosts = append(hosts, parsed)
		}
	}

	return hosts, nil
}

func loadHostDir(hostsDir string) ([]hostConfig, error) {
	b, err := os.ReadFile(filepath.Join(hostsDir, "hosts.toml"))
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return []hostConfig{}, nil
	}

	hosts, err := parseHostsFile(b)
	if err != nil {
		return nil, err
	}

	return hosts, nil
}

func LoadMirrorsConfig(mirrorsConfigDir, registryHost string) ([]MirrorConfig, error) {
	var mirrors []MirrorConfig

	if mirrorsConfigDir == "" {
		return mirrors, nil
	}
	hostDir, err := hostDirFromRoot(mirrorsConfigDir, registryHost)
	if err != nil {
		return nil, err
	}
	if hostDir == "" {
		return mirrors, nil
	}

	hostConfig, err := loadHostDir(hostDir)
	if err != nil {
		return nil, err
	}
	mirrors = append(mirrors, parseMirrorsConfig(hostConfig)...)

	return mirrors, nil
}
