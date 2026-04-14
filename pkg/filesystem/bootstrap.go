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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/pkg/layout"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

const (
	defaultBootstrapReadyAttempts = 20
	defaultBootstrapReadyInterval = 50 * time.Millisecond

	v6BlockBitsOffset = layout.RafsV6SuperBlockOffset + 12
	v6BlockBits512    = 9
	v6BlockBits4096   = 12
)

func waitForReadyBootstrap(path string) error {
	return waitForReadyBootstrapWithRetry(path, defaultBootstrapReadyAttempts, defaultBootstrapReadyInterval)
}

func waitForReadyBootstrapWithRetry(path string, attempts int, interval time.Duration) error {
	var (
		lastState *string
	)

	err := retry.Do(func() error {
		state, err := validateBootstrapAndBlobMeta(path)
		if err != nil {
			return err
		}

		// Require two consecutive "ready" states to reading unready bootstrap
		if lastState == nil || state != *lastState {
			lastState = &state
			return fmt.Errorf("bootstrap is not ready, current state: %s", state)
		}

		return nil
	},
		retry.Attempts(uint(attempts)),
		retry.Delay(interval),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(n uint, err error) {
			log.L.Warnf("bootstrap is not ready, retrying... attempt: %d, error: %v", n+1, err)
		}),
	)

	if err != nil {
		return fmt.Errorf("bootstrap is not ready after %d attempts, last error: %w", attempts, err)
	}

	return nil
}

func validateBootstrapAndBlobMeta(path string) (string, error) {
	bootstrapSize, err := validateBootstrap(path)
	if err != nil {
		return "", err
	}

	blobMetaState, err := validateBlobMetaFiles(path)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("bootstrap=%d;%s", bootstrapSize, blobMetaState), nil
}

func validateBootstrap(path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("bootstrap is not a regular file")
	}

	size := info.Size()
	headSize := int(size)
	if headSize > layout.MaxSuperBlockSize {
		headSize = layout.MaxSuperBlockSize
	}
	if headSize < 8 {
		return 0, fmt.Errorf("bootstrap is too small: %d", size)
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
		if size%int64(blockSize) != 0 {
			return 0, fmt.Errorf("v6 bootstrap size %d is not aligned to %d", size, blockSize)
		}
	}

	return size, nil
}

func validateBlobMetaFiles(bootstrapPath string) (string, error) {
	pattern := filepath.Join(filepath.Dir(bootstrapPath), "*.blob.meta")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	sort.Strings(matches)

	if len(matches) == 0 {
		return "blobmeta=none", nil
	}

	var b strings.Builder
	b.WriteString("blobmeta=")

	for i, p := range matches {
		file, err := os.Open(p)
		if err != nil {
			return "", err
		}

		info, err := file.Stat()
		if err != nil {
			file.Close()
			return "", err
		}
		if !info.Mode().IsRegular() {
			file.Close()
			return "", fmt.Errorf("blob.meta %s is not a regular file", p)
		}
		if info.Size() <= 0 {
			file.Close()
			return "", fmt.Errorf("blob.meta %s is empty", p)
		}

		var buf [1]byte
		if _, err := io.ReadFull(file, buf[:]); err != nil {
			file.Close()
			return "", fmt.Errorf("read blob.meta %s: %w", p, err)
		}
		file.Close()

		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(filepath.Base(p))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(info.Size(), 10))
	}

	return b.String(), nil
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
