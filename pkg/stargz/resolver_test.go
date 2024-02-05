/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package stargz

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/containerd/nydus-snapshotter/pkg/auth"
)

func TestResolver_resolve(t *testing.T) {

	resolver := Resolver{
		res: &MockResolver{},
	}
	keychain, err := auth.FromBase64("dGVzdDp0ZXN0Cg==")
	require.Nil(t, err)
	sr, err := resolver.resolve("example.com/test/myserver:latest-stargz", "sha256:mock", keychain)
	require.Nil(t, err)
	size := sr.Size()
	var b [47]byte
	n, err := sr.ReadAt(b[:], size-47)
	assert.Nil(t, err)
	assert.Equal(t, 47, n)
	tocOffset, ok := parseFooter(b[:])
	assert.True(t, ok)
	fmt.Printf("tocoffset %d", tocOffset)
	tocTargz := make([]byte, size-tocOffset-47)
	_, err = sr.ReadAt(tocTargz, tocOffset)
	assert.Nil(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(tocTargz))
	assert.Nil(t, err)
	zr.Multistream(false)
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	assert.Nil(t, err)
	assert.Equal(t, "stargz.index.json", h.Name)
}

type MockResolver struct {
}

func (res *MockResolver) Resolve(_ name.Reference, _ string, _ authn.Keychain) (string, http.RoundTripper, error) {
	return "http://oss.com/v2/test/myserver/blobs/sha256:mock", &mockRoundTripper{}, nil
}

type mockRoundTripper struct{}

func (tr *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	if url == "https://example.com/v2/" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	}
	if url == "https://example.com/v2/test/myserver/blobs/sha256:mock" {
		header := make(http.Header)
		header.Add("Location", "http://oss.com/v2/test/myserver/blobs/sha256:mock")
		return &http.Response{
			StatusCode: http.StatusMovedPermanently,
			Header:     header,
			Body:       io.NopCloser(bytes.NewReader([]byte{})),
		}, nil
	}
	if url == "http://oss.com/v2/test/myserver/blobs/sha256:mock" {
		rangeHeader := req.Header.Get("Range")
		// get length
		if rangeHeader == "bytes=0-0" {
			header := make(http.Header)
			header.Add("Content-Range", "bytes 0-0/24613186")
			return &http.Response{
				StatusCode: http.StatusPartialContent,
				Header:     header,
				Body:       io.NopCloser(bytes.NewReader([]byte{})),
			}, nil
		}
		// get footer
		if rangeHeader == "bytes=24613139-24613185" {
			footer, _ := os.ReadFile("testdata/stargzfooter.bin")
			return &http.Response{
				StatusCode: http.StatusPartialContent,
				Body:       io.NopCloser(bytes.NewReader(footer)),
			}, nil
		}
		if rangeHeader == "bytes=24442675-24613138" {
			toc, _ := os.ReadFile("testdata/stargztoc.bin")
			return &http.Response{
				StatusCode: http.StatusPartialContent,
				Body:       io.NopCloser(bytes.NewReader(toc)),
			}, nil
		}
	}

	return &http.Response{
		StatusCode: 200,
	}, nil
}
