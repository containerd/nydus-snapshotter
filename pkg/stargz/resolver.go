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
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/utils/transport"
	"github.com/containerd/stargz-snapshotter/estargz"
	distribution "github.com/distribution/reference"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/pkg/errors"
)

const httpTimeout = 15 * time.Second

const (
	FooterSize  = 47
	TocFileName = "stargz.index.json"
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

type Blob struct {
	ref    string
	digest string
	sr     *io.SectionReader
}

// getTocOffset get toc offset from stargz footer
func (bb *Blob) GetTocOffset() (int64, error) {
	tocOffset, _, err := estargz.OpenFooter(bb.sr)
	if err != nil {
		return 0, errors.Wrap(err, "open stargz blob footer")
	}

	return tocOffset, nil
}

// ReadToc read stargz toc content from blob
func (bb *Blob) ReadToc() (io.Reader, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("read toc duration %d", duration.Milliseconds())
	}()

	tocOffset, err := bb.GetTocOffset()
	if err != nil {
		return nil, err
	}
	tocBuf := make([]byte, bb.sr.Size()-tocOffset-FooterSize)
	_, err = bb.sr.ReadAt(tocBuf, tocOffset)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(tocBuf))
	if err != nil {
		return nil, err
	}
	zr.Multistream(false)
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	if err != nil {
		return nil, err
	}
	if h.Name != TocFileName {
		return nil, fmt.Errorf("Failed to find toc from image %s blob %s", bb.ref, bb.digest)
	}
	var buf bytes.Buffer
	_, err = buf.ReadFrom(tr)
	if err != nil {
		return nil, err
	}
	return &buf, nil
}

func (bb *Blob) GetDigest() string {
	return bb.digest
}

func (bb *Blob) GetImageReference() string {
	return bb.ref
}

func (r *Resolver) GetBlob(ref, digest string, keychain authn.Keychain) (*Blob, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("get blob duration %d", duration.Milliseconds())
	}()

	sr, err := r.resolve(ref, digest, keychain)
	if err != nil {
		return nil, err
	}
	return &Blob{
		ref:    ref,
		digest: digest,
		sr:     sr,
	}, nil
}

type readerAtFunc func([]byte, int64) (int, error)

func (f readerAtFunc) ReadAt(p []byte, offset int64) (int, error) { return f(p, offset) }

// parseFooter extract toc offset from footer
func parseFooter(p []byte) (tocOffset int64, ok bool) {
	if len(p) != 47 {
		return 0, false
	}
	zr, err := gzip.NewReader(bytes.NewReader(p))
	if err != nil {
		return 0, false
	}
	extra := zr.Header.Extra
	if len(extra) != 16+len("STARGZ") {
		return 0, false
	}
	if string(extra[16:]) != "STARGZ" {
		return 0, false
	}
	tocOffset, err = strconv.ParseInt(string(extra[:16]), 16, 64)
	return tocOffset, err == nil
}

func (r *Resolver) resolve(ref, digest string, keychain authn.Keychain) (*io.SectionReader, error) {
	named, err := distribution.ParseDockerRef(ref)
	if err != nil {
		return nil, err
	}
	host := distribution.Domain(named)
	sref := fmt.Sprintf("%s/%s", host, distribution.Path(named))
	nref, err := name.ParseReference(sref)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse ref %q (%q)", sref, digest)
	}

	url, tr, err := r.res.Resolve(nref, digest, keychain)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to resolve reference of %q, %q", nref, digest)
	}

	size, err := getSize(url, tr)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get size from url %s", url)
	}
	log.L.Infof("get size %d", size)

	sr := io.NewSectionReader(readerAtFunc(func(b []byte, offset int64) (int, error) {
		length := len(b)
		ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return 0, err
		}
		req.Close = false
		r := fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length)-1)
		req.Header.Set("Range", r)
		res, err := tr.RoundTrip(req)
		if err != nil {
			return 0, err
		}
		defer func() {
			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				log.L.Errorf("failed to copy %s", err)
			}
			res.Body.Close()
		}()
		if res.StatusCode/100 != 2 {
			return 0, fmt.Errorf("failed to HEAD request with code %d", res.StatusCode)
		}
		return io.ReadFull(res.Body, b)
	}), 0, size)

	return sr, nil
}

func getSize(url string, tr http.RoundTripper) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Close = false
	req.Header.Set("Range", "bytes=0-0")
	res, err := tr.RoundTrip(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			log.L.Errorf("failed to copy %s", err)
		}
		res.Body.Close()
	}()
	if res.StatusCode/100 != 2 {
		return 0, fmt.Errorf("failed to HEAD request with code %d", res.StatusCode)
	}
	contentRange := res.Header.Get("Content-Range")
	totalSize := strings.Split(contentRange, "/")[1]
	return strconv.ParseInt(totalSize, 10, 64)
}
