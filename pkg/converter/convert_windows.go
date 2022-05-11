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
)

func Convert(ctx context.Context, dest io.Writer, opt ConvertOption) (io.WriteCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func Merge(ctx context.Context, layers []Layer, dest io.Writer, opt MergeOption) error {
	return fmt.Errorf("not implemented")
}
