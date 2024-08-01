/*
 * Copyright (c) 2021. Alibaba Cloud. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package resolve

import (
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/utils/transport"
	distribution "github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/name"
	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/pkg/errors"
)

type Resolver struct {
	res transport.Resolve
}

func NewResolver() *Resolver {
	resolver := Resolver{
		res: transport.NewPool(),
	}
	return &resolver
}

func (r *Resolver) Resolve(ref, digest string, labels map[string]string) (io.ReadCloser, error) {
	named, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed parse docker ref %s", ref)
	}
	host := distribution.Domain(named)
	sref := fmt.Sprintf("%s/%s", host, distribution.Path(named))
	nref, err := name.ParseReference(sref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse ref %q (%q)", sref, digest)
	}
	keychain := auth.GetRegistryKeyChain(host, ref, labels)

	var tr http.RoundTripper
	url, tr, err := r.res.Resolve(nref, digest, keychain)

	if err != nil {
		return nil, errors.Wrapf(err, "failed to create authn transport %v", keychain)
	}

	req, err := retryablehttp.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to new http get %s", url)
	}

	client := newRetryHTTPClient(tr)
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to http get %s", url)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to GET request with code %d", res.StatusCode)
	}
	return res.Body, nil
}

func newRetryHTTPClient(tr http.RoundTripper) *retryablehttp.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient.Transport = tr
	retryClient.Logger = nil
	return retryClient
}
