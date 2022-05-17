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
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
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

func (r *Resolver) Resolve(ref, digest string, labels map[string]string) (io.ReadCloser, error) {
	named, err := docker.ParseDockerRef(ref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed parse docker ref %s", ref)
	}
	host := docker.Domain(named)
	sref := fmt.Sprintf("%s/%s", host, docker.Path(named))
	nref, err := name.ParseReference(sref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse ref %q (%q)", sref, digest)
	}
	keychain := auth.GetRegistryKeyChain(host, labels)

	var tr http.RoundTripper
	if keychain == nil {
		// Fallback to use no auth RoundTripper
		log.L.Infof("fallback to use no auth round tripper")
		tr = r.transport
	} else {
		tr, err = registry.AuthnTransport(nref, r.transport, keychain)
		if err != nil {
			return nil, errors.Wrapf(err, "failt to create authn transport %v", keychain)
		}
	}
	url := fmt.Sprintf("%s://%s/v2/%s/blobs/%s",
		nref.Context().Registry.Scheme(),
		nref.Context().RegistryStr(),
		nref.Context().RepositoryStr(),
		digest)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "faild to new http get %s", url)
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   time.Second * 30,
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "faild to http get %s", url)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to  GET request with code %d", res.StatusCode)
	}
	return res.Body, nil
}
