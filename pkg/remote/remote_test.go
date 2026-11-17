/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package remote

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes/docker"
	"github.com/stretchr/testify/assert"
)

func TestRetryWithPlainHTTP(t *testing.T) {
	const host = "myregistry.example.com"
	ref := fmt.Sprintf("%s/repo/image:latest", host)
	httpResponseErr := fmt.Errorf("Get https://%s/v2/: server gave HTTP response to HTTPS client", host)
	connRefusedErr := fmt.Errorf("Get https://%s/v2/: connect: connection refused", host)
	otherErr := fmt.Errorf("some unrelated error")

	tests := []struct {
		name     string
		insecure bool
		err      error
		want     bool
	}{
		{
			name:     "insecure allows HTTP fallback on HTTP response error",
			insecure: true,
			err:      httpResponseErr,
			want:     true,
		},
		{
			name:     "insecure allows HTTP fallback on connection refused",
			insecure: true,
			err:      connRefusedErr,
			want:     true,
		},
		{
			name:     "insecure does not fallback on unrelated error",
			insecure: true,
			err:      otherErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on HTTP response error",
			insecure: false,
			err:      httpResponseErr,
			want:     false,
		},
		{
			name:     "secure blocks HTTP fallback on connection refused",
			insecure: false,
			err:      connRefusedErr,
			want:     false,
		},
		{
			name:     "nil error returns false",
			insecure: true,
			err:      nil,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Remote{insecure: tt.insecure}
			got := r.RetryWithPlainHTTP(ref, tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRegistryHostsDefaultDockerIOMapping(t *testing.T) {
	// No certs.d override: keep the pre-existing docker.io behavior.
	hosts, err := newRegistryHosts(nil, false, false, []string{t.TempDir()})("docker.io")
	assert.NoError(t, err)
	assert.Len(t, hosts, 1)
	assert.Equal(t, "https", hosts[0].Scheme)
	assert.Equal(t, "registry-1.docker.io", hosts[0].Host)
	assert.Equal(t, "/v2", hosts[0].Path)
	assert.Equal(t, docker.HostCapabilityPull|docker.HostCapabilityResolve|docker.HostCapabilityPush, hosts[0].Capabilities)

	// A non-docker.io host keeps the pre-existing direct mapping.
	hosts, err = newRegistryHosts(nil, false, false, []string{t.TempDir()})("registry.example.com")
	assert.NoError(t, err)
	assert.Len(t, hosts, 1)
	assert.Equal(t, "https", hosts[0].Scheme)
	assert.Equal(t, "registry.example.com", hosts[0].Host)
}

func TestRegistryHostsHonorsCertsDHostsTOML(t *testing.T) {
	root := t.TempDir()
	hostDir := filepath.Join(root, "docker.io")
	assert.NoError(t, os.MkdirAll(hostDir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(hostDir, "hosts.toml"), []byte(`
server = "https://registry-1.docker.io"

[host."http://mirror.local:65001"]
  capabilities = ["pull", "resolve"]
  [host."http://mirror.local:65001".header]
    X-Dragonfly-Registry = "https://registry-1.docker.io"
`), 0o644))

	hosts, err := newRegistryHosts(nil, false, false, []string{root})("docker.io")
	assert.NoError(t, err)
	assert.Len(t, hosts, 2)

	mirror := hosts[0]
	assert.Equal(t, "http", mirror.Scheme)
	assert.Equal(t, "mirror.local:65001", mirror.Host)
	assert.Equal(t, "/v2", mirror.Path)
	assert.True(t, mirror.Capabilities.Has(docker.HostCapabilityPull))
	assert.True(t, mirror.Capabilities.Has(docker.HostCapabilityResolve))
	assert.Equal(t, "https://registry-1.docker.io", mirror.Header.Get("X-Dragonfly-Registry"))

	// The docker.io -> registry-1.docker.io mapping is only used when it is
	// not overridden by certs.d (the certs.d server is the last host).
	assert.Equal(t, "registry-1.docker.io", hosts[1].Host)
}

func TestCertsDHostDirFallsThroughRoots(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	assert.NoError(t, os.MkdirAll(filepath.Join(second, "docker.io"), 0o755))

	dir, err := certsDHostDir([]string{first, second})("docker.io")
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(second, "docker.io"), dir)

	_, err = certsDHostDir([]string{first})("unknown.example.com")
	assert.True(t, errdefs.IsNotFound(err))
}
