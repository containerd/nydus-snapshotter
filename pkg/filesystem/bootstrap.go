/*
 * Copyright (c) 2026. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/layout"
)

const (
	defaultBootstrapReadyAttempts = 20
	defaultBootstrapReadyInterval = 50 * time.Millisecond
	v6BlockBitsOffset             = layout.RafsV6SuperBlockOffset + 12
	v6BlockBits512                = 9
	v6BlockBits4096               = 12
)

func waitForReadyBootstrap(path string) error {
	return waitForReadyBootstrapWithRetry(path, defaultBootstrapReadyAttempts, defaultBootstrapReadyInterval)
}

func waitForReadyBootstrapWithRetry(path string, attempts int, interval time.Duration) error {
	var (
		lastSize int64 = -1
		lastErr  error
	)

	for idx := 0; idx < attempts; idx++ {
		size, err := validateBootstrap(path)
		if err == nil {
			if size == lastSize {
				return nil
			}
			lastSize = size
			lastErr = fmt.Errorf("bootstrap size %d is still changing", size)
		} else {
			log.L.WithError(err).Warnf("bootstrap %s validation attempt %d/%d failed", path, idx+1, attempts)
			lastErr = err
		}

		if idx+1 < attempts {
			time.Sleep(interval)
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("bootstrap is not ready")
	}

	return fmt.Errorf("bootstrap %s is not ready: %w", path, lastErr)
}

func validateBootstrap(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("bootstrap is not a regular file")
	}

	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	headSize := int(info.Size())
	if headSize > layout.MaxSuperBlockSize {
		headSize = layout.MaxSuperBlockSize
	}
	if headSize < 8 {
		return 0, fmt.Errorf("bootstrap is too small: %d", info.Size())
	}

	head := make([]byte, headSize)
	if _, err := io.ReadFull(file, head); err != nil {
		return 0, err
	}

	version, err := layout.DetectFsVersion(head)
	if err != nil {
		return 0, err
	}

	if version == layout.RafsV6 {
		blockSize, err := detectV6BlockSize(head)
		if err != nil {
			return 0, err
		}
		if info.Size()%int64(blockSize) != 0 {
			return 0, fmt.Errorf("v6 bootstrap size %d is not aligned to %d", info.Size(), blockSize)
		}
	}

	return info.Size(), nil
}

func detectV6BlockSize(head []byte) (int, error) {
	if len(head) <= int(v6BlockBitsOffset) {
		return 0, fmt.Errorf("v6 bootstrap header is too small")
	}

	switch head[v6BlockBitsOffset] {
	case v6BlockBits512:
		return 512, nil
	case v6BlockBits4096:
		return 4096, nil
	default:
		return 0, fmt.Errorf("unknown v6 block bits %d", head[v6BlockBitsOffset])
	}
}
