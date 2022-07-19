/* SPDX-License-Identifier: Apache-2.0 */
/* Copyright (c) 2022. Alibaba Cloud, Ant Group. All rights reserved. */

package converter

import (
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	dockerconfig "github.com/docker/cli/cli/config"
)

func newDefaultClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 5 * time.Second,
			DisableKeepAlives:     true,
			TLSNextProto:          make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		},
	}
}

// withCredentialFunc accepts host url parameter and returns with
// username, password and error.
type withCredentialFunc = func(string) (string, string, error)

// NewDockerConfigCredFunc attempts to read docker auth config file `$DOCKER_CONFIG/config.json`
// to communicate with remote registry, `$DOCKER_CONFIG` defaults to `~/.docker`.
func NewDockerConfigCredFunc() withCredentialFunc {
	return func(host string) (string, string, error) {
		// The host of docker hub image will be converted to `registry-1.docker.io` in:
		// github.com/containerd/containerd/remotes/docker/registry.go
		// But we need use the key `https://index.docker.io/v1/` to find auth from docker config.
		if host == "registry-1.docker.io" {
			host = "https://index.docker.io/v1/"
		}

		config := dockerconfig.LoadDefaultConfigFile(os.Stderr)
		authConfig, err := config.GetAuthConfig(host)
		if err != nil {
			return "", "", err
		}

		return authConfig.Username, authConfig.Password, nil
	}
}

func NewResolver(credFunc withCredentialFunc) remotes.Resolver {
	registryHosts := docker.ConfigureDefaultRegistries(
		docker.WithAuthorizer(docker.NewAuthorizer(
			newDefaultClient(),
			credFunc,
		)),
		docker.WithClient(newDefaultClient()),
		docker.WithPlainHTTP(func(host string) (bool, error) {
			_insecure, err := docker.MatchLocalhost(host)
			if err != nil {
				return false, err
			}
			if _insecure {
				return true, nil
			}
			return false, nil
		}),
	)

	return docker.NewResolver(docker.ResolverOptions{
		Hosts: registryHosts,
	})
}
