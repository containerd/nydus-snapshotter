/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package registry

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	distribution "github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/pkg/errors"
)

type Image struct {
	Host string
	Repo string
}

func ConvertToVPCHost(registryHost string) string {
	parts := strings.Split(registryHost, ".")
	if strings.HasSuffix(parts[0], "-vpc") {
		return registryHost
	}
	parts[0] = fmt.Sprintf("%s-vpc", parts[0])
	return strings.Join(parts, ".")
}

func ParseImage(imageID string) (Image, error) {
	named, err := distribution.ParseDockerRef(imageID)
	if err != nil {
		return Image{}, err
	}
	host := distribution.Domain(named)
	repo := distribution.Path(named)
	return Image{
		Host: host,
		Repo: repo,
	}, nil
}

func ParseLabels(labels map[string]string) (rRef, rDigest string) {
	if ref, ok := labels[snpkg.TargetRefLabel]; ok {
		rRef = ref
	}
	if layerDigest, ok := labels[snpkg.TargetLayerDigestLabel]; ok {
		rDigest = layerDigest
	}
	return
}

func AuthnTransport(ref name.Reference, tr http.RoundTripper, keychain authn.Keychain) (http.RoundTripper, error) {
	var err error
	var auth authn.Authenticator

	if keychain == nil || (reflect.ValueOf(keychain).Kind() == reflect.Ptr && reflect.ValueOf(keychain).IsNil()) {
		auth = authn.Anonymous
	} else {
		auth, err = keychain.Resolve(ref.Context())
		if err != nil {
			return nil, errors.Wrapf(err, "failed to resolve reference %q", ref)
		}
	}

	errCh := make(chan error)
	var rTr http.RoundTripper
	go func() {
		rTr, err = transport.NewWithContext(
			context.TODO(),
			ref.Context().Registry,
			auth,
			tr,
			[]string{ref.Scope(transport.PullScope)},
		)
		errCh <- err
	}()
	select {
	case err = <-errCh:
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("authentication timeout")
	}
	return rTr, err
}
