/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"context"
	"fmt"
	"io"
)

type readCloser struct {
	io.ReadCloser
	action func() error
}

func (c *readCloser) Close() error {
	if err := c.ReadCloser.Close(); err != nil {
		return err
	}
	return c.action()
}

func newReadCloser(rc io.ReadCloser, action func() error) *readCloser {
	return &readCloser{
		ReadCloser: rc,
		action:     action,
	}
}

type writeCloser struct {
	io.WriteCloser
	action func() error
}

func (c *writeCloser) Close() error {
	if err := c.action(); err != nil {
		return err
	}
	return c.WriteCloser.Close()
}

func newWriteCloser(wc io.WriteCloser, action func() error) *writeCloser {
	return &writeCloser{
		WriteCloser: wc,
		action:      action,
	}
}

type seekReader struct {
	io.ReaderAt
	pos int64
}

func (ra *seekReader) Read(p []byte) (int, error) {
	n, err := ra.ReaderAt.ReadAt(p, ra.pos)
	ra.pos += int64(len(p))
	return n, err
}

func (ra *seekReader) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekCurrent {
		return 0, fmt.Errorf("only support SeekCurrent whence")
	}
	ra.pos += offset
	return ra.pos, nil
}

func newSeekReader(ra io.ReaderAt) *seekReader {
	return &seekReader{
		ReaderAt: ra,
		pos:      0,
	}
}

type ctxReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *ctxReader) Read(p []byte) (n int, err error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func newCtxReader(ctx context.Context, reader io.Reader) io.Reader {
	return &ctxReader{
		ctx:    ctx,
		reader: reader,
	}
}
