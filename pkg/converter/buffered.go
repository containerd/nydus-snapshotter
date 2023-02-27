/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package converter

import (
	"bufio"
	"io"
	"sync"
)

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/archive/compression/compression.go#L57
var (
	bufioReader32KPool = &sync.Pool{
		New: func() interface{} { return bufio.NewReaderSize(nil, 32*1024) },
	}
)

type bufferedReader struct {
	buf *bufio.Reader
}

func newBufferedReader(r io.Reader) *bufferedReader {
	buf := bufioReader32KPool.Get().(*bufio.Reader)
	buf.Reset(r)
	return &bufferedReader{buf}
}

func (r *bufferedReader) Read(p []byte) (n int, err error) {
	if r.buf == nil {
		return 0, io.EOF
	}
	n, err = r.buf.Read(p)
	if err == io.EOF {
		r.Put()
	}
	return
}

func (r *bufferedReader) Put() {
	if r.buf == nil {
		return
	}
	r.buf.Reset(nil)
	bufioReader32KPool.Put(r.buf)
	r.buf = nil
}

func (r *bufferedReader) Peek(n int) ([]byte, error) {
	if r.buf == nil {
		return nil, io.EOF
	}
	return r.buf.Peek(n)
}
