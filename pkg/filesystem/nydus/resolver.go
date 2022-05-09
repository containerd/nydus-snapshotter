/*
 * Copyright (c) 2021. Alibaba Cloud. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package nydus

import (
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"
)

type Resolver struct {
	transport http.RoundTripper
}

func NewResolver() *Resolver {
	resolver := Resolver{
		transport: http.DefaultTransport,
	}
	return &resolver
}

func (r *Resolver) Resolve(ref, digest string, keychain authn.Keychain) (io.ReadCloser, error) {
	named, err := docker.ParseDockerRef(ref)
	if err != nil {
		return nil, err
	}
	host := docker.Domain(named)
	sref := fmt.Sprintf("%s/%s", host, docker.Path(named))
	nref, err := name.ParseReference(sref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse ref %q (%q)", sref, digest)
	}

	var tr http.RoundTripper
	if nref.Context().Registry.Scheme() == "https" {
		tr, err = registry.AuthnTransport(nref, r.transport, keychain)
		if err != nil {
			return nil, err
		}
	} else {
		// Use default transport
		tr = r.transport
	}
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s",
		nref.Context().Registry.Scheme(),
		nref.Context().RegistryStr(),
		nref.Context().RepositoryStr(),
		digest)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Transport: tr}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to  GET request with code %d", res.StatusCode)
	}
	return res.Body, nil
}
