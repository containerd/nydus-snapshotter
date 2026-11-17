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
	"strings"

	"github.com/containerd/errdefs"
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes/docker"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes/docker/config"
	"github.com/distribution/reference"
	"github.com/pkg/errors"
)

// containerd reads registry mirrors, TLS certificates and headers from
// certs.d. RKE2 keeps its containerd state under /var/lib/rancher/rke2, while
// standalone containerd installations use /etc/containerd. Keep the same
// lookup order as the node kubelet/containerd layout.
var defaultCertsDRoots = []string{
	"/var/lib/rancher/rke2/agent/etc/containerd/certs.d",
	"/etc/containerd/certs.d",
}

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
	// insecure indicates that the registry is explicitly configured as insecure.
	// HTTP fallback is only allowed when insecure is true,
	// matching Docker's --insecure-registry semantics.
	insecure bool
	// certsDRoots are the containerd certs.d roots inspected for hosts.toml.
	certsDRoots []string
}

// Option customizes a Remote.
type Option func(*Remote)

// WithCertsDRoots overrides the containerd certs.d lookup roots. It is useful
// for tests and for nodes with a non-standard containerd state directory.
func WithCertsDRoots(roots ...string) Option {
	return func(r *Remote) {
		r.certsDRoots = roots
	}
}

func New(keyChain *auth.PassKeyChain, insecure bool, opts ...Option) *Remote {
	r := &Remote{
		insecure:    insecure,
		certsDRoots: defaultCertsDRoots,
	}
	for _, opt := range opts {
		opt(r)
	}

	r.resolverFunc = func(plainHTTP bool) remotes.Resolver {
		return docker.NewResolver(docker.ResolverOptions{
			Hosts: newRegistryHosts(keyChain, insecure, plainHTTP, r.certsDRoots),
		})
	}

	return r
}

// newRegistryHosts builds a resolver host list from containerd certs.d,
// including hosts.toml server/host entries, capabilities and headers. When no
// certs.d override exists it falls back to the previous default behavior
// (docker.io maps to registry-1.docker.io).
func newRegistryHosts(keyChain *auth.PassKeyChain, insecure, plainHTTP bool, certsDRoots []string) docker.RegistryHosts {
	scheme := "https"
	if plainHTTP {
		scheme = "http"
	}

	return config.ConfigureHosts(context.Background(), config.HostOptions{
		HostDir: certsDHostDir(certsDRoots),
		Credentials: func(string) (string, string, error) {
			if keyChain == nil {
				return "", "", nil
			}
			return keyChain.Username, keyChain.Password, nil
		},
		DefaultScheme: scheme,
		DefaultTLS: &tls.Config{
			InsecureSkipVerify: insecure,
		},
	})
}

// certsDHostDir returns the first existing containerd certs.d host directory
// from the configured roots.
func certsDHostDir(roots []string) func(string) (string, error) {
	lookups := make([]func(string) (string, error), 0, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		lookups = append(lookups, config.HostDirFromRoot(root))
	}

	return func(host string) (string, error) {
		for _, lookup := range lookups {
			dir, err := lookup(host)
			if err == nil {
				return dir, nil
			}
			if !errdefs.IsNotFound(err) {
				return "", err
			}
		}
		return "", errdefs.ErrNotFound
	}
}

func (remote *Remote) RetryWithPlainHTTP(ref string, err error) bool {
	if !remote.insecure {
		return false
	}

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
