/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package remote

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes/docker"
	"github.com/distribution/reference"
	"github.com/pkg/errors"
)

// IsErrHTTPResponseToHTTPSClient returns whether err is
// "http: server gave HTTP response to HTTPS client"
func isErrHTTPResponseToHTTPSClient(err error) bool {
	// The error string is unexposed as of Go 1.16, so we can't use `errors.Is`.
	// https://github.com/golang/go/issues/44855
	const unexposed = "server gave HTTP response to HTTPS client"
	return strings.Contains(err.Error(), unexposed)
}

// IsErrConnectionRefused return whether err is
// "connect: connection refused"
func isErrConnectionRefused(err error) bool {
	const errMessage = "connect: connection refused"
	return strings.Contains(err.Error(), errMessage)
}

type Remote struct {
	// The resolver is used for image pull or fetches requests. The best practice
	// in containerd is that each resolver instance is used only once for a request
	// and is destroyed when the request completes. When a registry token expires,
	// the resolver does not re-apply for a new token, so it's better to create a
	// new resolver instance using resolverFunc for each request.
	resolverFunc func(plainHTTP bool) remotes.Resolver
	// withPlainHTTP attempts to request the remote registry using http instead
	// of https.
	withPlainHTTP bool
}

func New(keyChain *auth.PassKeyChain, insecure bool) *Remote {
	// nolint:unparam
	credFunc := func(string) (string, string, error) {
		if keyChain == nil {
			return "", "", nil
		}
		return keyChain.Username, keyChain.Password, nil
	}

	newClient := func(insecure bool) *http.Client {
		client := http.DefaultClient
		transport := http.DefaultTransport.(*http.Transport)
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecure,
		}
		client.Transport = transport
		return client
	}

	resolverFunc := func(plainHTTP bool) remotes.Resolver {
		registryHosts := docker.ConfigureDefaultRegistries(
			docker.WithAuthorizer(
				docker.NewDockerAuthorizer(
					docker.WithAuthClient(newClient(insecure)),
					docker.WithAuthCreds(credFunc),
				),
			),
			docker.WithClient(newClient(insecure)),
			docker.WithPlainHTTP(func(_ string) (bool, error) {
				return plainHTTP, nil
			}),
		)

		return docker.NewResolver(docker.ResolverOptions{
			Hosts: registryHosts,
		})
	}

	return &Remote{
		resolverFunc:  resolverFunc,
		withPlainHTTP: false,
	}
}

func (remote *Remote) RetryWithPlainHTTP(ref string, err error) bool {
	retry := err != nil && (isErrHTTPResponseToHTTPSClient(err) || isErrConnectionRefused(err))
	if !retry {
		return false
	}

	parsed, _ := reference.ParseNormalizedNamed(ref)
	if parsed != nil {
		host := reference.Domain(parsed)
		// If the error message includes the current registry host string, it
		// implies that we can retry the request with plain HTTP.
		if strings.Contains(err.Error(), fmt.Sprintf("/%s/", host)) {
			log.G(context.TODO()).WithError(err).Warningf("retrying with http for %s", host)
			remote.withPlainHTTP = true
		}
	}

	return remote.withPlainHTTP
}

func (remote *Remote) Resolve(_ context.Context, _ string) remotes.Resolver {
	return remote.resolverFunc(remote.withPlainHTTP)
}

func (remote *Remote) Fetcher(ctx context.Context, ref string) (remotes.Fetcher, error) {
	resolver := remote.Resolve(ctx, ref)
	fetcher, err := resolver.Fetcher(ctx, ref)
	if err != nil {
		return nil, errors.Wrap(err, "get fetcher")
	}
	return fetcher, nil
}
