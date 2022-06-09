/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"os"

	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/sirupsen/logrus"
)

const (
	dockerHost          = "https://index.docker.io/v1/"
	convertedDockerHost = "registry-1.docker.io"
)

// FromDockerConfig finds auth for a given host in docker's config.json settings.
func FromDockerConfig(host string) *PassKeyChain {
	if len(host) == 0 {
		return nil
	}

	// The host of docker hub image will be converted to `registry-1.docker.io` in:
	// github.com/containerd/containerd/remotes/docker/registry.go
	// But we need use the key `https://index.docker.io/v1/` to find auth from docker config.
	if host == convertedDockerHost {
		host = dockerHost
	}

	config := dockerconfig.LoadDefaultConfigFile(os.Stderr)
	authConfig, err := config.GetAuthConfig(host)
	if err != nil {
		logrus.WithError(err).Infof("no auth from docker config for host %s", host)
		return nil
	}

	// if user login registry, the auth info is not empty.
	if len(authConfig.Auth) > 0 {
		passKeyChain, err := FromBase64(authConfig.Auth)
		if err != nil {
			logrus.WithError(err).Errorf("decode username & password failed from base64")
			return nil
		}
		return &passKeyChain
	}

	// if user don't  login registry, and without username or password return nil.
	if len(authConfig.Username) == 0 || len(authConfig.Password) == 0 {
		return nil
	}

	return &PassKeyChain{
		Username: authConfig.Username,
		Password: authConfig.Password,
	}
}
