/*
 * Copyright (c) 2026. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package auth

import (
	"github.com/containerd/containerd/v2/pkg/reference"
	distribution "github.com/distribution/reference"
	"github.com/pkg/errors"
)

// AuthRequest contains parameters for retrieving registry credentials.
type AuthRequest struct {
	// Ref is the full image reference (e.g., "docker.io/library/nginx:latest")
	Ref string
	// Labels are snapshot labels that may contain credentials
	Labels map[string]string
}

// AuthProvider manage how credentials are retrieved for different sources
type AuthProvider interface {
	// GetCredentials retrieves credentials for the given request.
	// Returns nil if no credentials are available.
	GetCredentials(req *AuthRequest) (*PassKeyChain, error)
}

// parseReference returns the reference.Spec and host for the given reference
func parseReference(ref string) (refSpec reference.Spec, host string, err error) {
	namedRef, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return reference.Spec{}, "", errors.Wrap(err, "parse docker reference")
	}

	refSpec, err = reference.Parse(namedRef.String())
	if err != nil {
		return reference.Spec{}, "", errors.Wrap(err, "parse image reference")
	}

	host = distribution.Domain(namedRef)
	if host == "" {
		return reference.Spec{}, "", errors.New("host not found in ref")
	}

	return refSpec, host, nil
}
