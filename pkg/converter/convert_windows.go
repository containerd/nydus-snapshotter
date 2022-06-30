//go:build windows
// +build windows

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

	"github.com/containerd/containerd/content"
)

func Pack(ctx context.Context, dest io.Writer, opt PackOption) (io.WriteCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func Merge(ctx context.Context, layers []Layer, dest io.Writer, opt MergeOption) error {
	return fmt.Errorf("not implemented")
}

func Unpack(ctx context.Context, ia content.ReaderAt, dest io.Writer, opt UnpackOption) error {
	return fmt.Errorf("not implemented")
}
