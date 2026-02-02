/*
 * Copyright (c) 2021. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"fmt"
	"os"

	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/pkg/errors"
)

const (
	dockerHost          = "https://index.docker.io/v1/"
	convertedDockerHost = "registry-1.docker.io"
)

// DockerProvider retrieves credentials from Docker's config.json.
type DockerProvider struct {
	dockerConfig *configfile.ConfigFile
}

// NewDockerProvider creates a new Docker config-based auth provider.
func NewDockerProvider() *DockerProvider {
	return &DockerProvider{
		dockerConfig: dockerconfig.LoadDefaultConfigFile(os.Stderr),
	}
}

// GetCredentials retrieves credentials from Docker's config.json.
// Returns nil if no credentials are found for the registry.
func (p *DockerProvider) GetCredentials(req *AuthRequest) (*PassKeyChain, error) {
	if req == nil || req.Ref == "" {
		return nil, errors.New("ref not found in request")
	}

	_, host, err := parseReference(req.Ref)
	if err != nil {
		return nil, errors.Wrapf(err, "parse reference %s", req.Ref)
	}

	// The host of docker hub image will be converted to `registry-1.docker.io` in:
	// github.com/containerd/containerd/remotes/docker/registry.go
	// But we need use the key `https://index.docker.io/v1/` to find auth from docker config.
	if host == convertedDockerHost {
		host = dockerHost
	}

	authConfig, err := p.dockerConfig.GetAuthConfig(host)
	if err != nil {
		return nil, errors.Wrapf(err, "no auth from docker config for host %s", host)
	}

	// Do not return partially empty auth. It makes caller life easier.
	if len(authConfig.Username) == 0 || len(authConfig.Password) == 0 {
		return nil, fmt.Errorf("auth config not complete for host: %s", host)
	}

	return &PassKeyChain{
		Username: authConfig.Username,
		Password: authConfig.Password,
	}, nil
}
