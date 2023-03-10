/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package mount

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

type Interface interface {
	Umount(target string) error
}

type Mounter struct {
}

func (m *Mounter) Umount(target string) error {
	if mounted, err := IsMountpoint(target); err == nil {
		if !mounted {
			return errors.New("not mounted")
		}
	} else {
		return err
	}

	// return syscall.Unmount(target, syscall.MNT_FORCE)
	return syscall.Unmount(target, 0)
}

func NormalizePath(path string) (realPath string, err error) {
	if realPath, err = filepath.Abs(path); err != nil {
		return "", errors.Wrapf(err, "get absolute path for %s", path)
	}
	if realPath, err = filepath.EvalSymlinks(realPath); err != nil {
		return "", errors.Wrapf(err, "canonicalise path for %s", path)
	}
	if _, err := os.Stat(realPath); err != nil {
		return "", errors.Wrapf(err, "stat target of %s", path)
	}
	return realPath, nil
}

// return value `true` means the path is mounted
func IsMountpoint(path string) (bool, error) {
	realPath, err := NormalizePath(path)
	if err != nil {
		return false, err
	}

	if path == "/" {
		return true, nil
	}

	stat, err := os.Stat(realPath)
	if err != nil {
		return false, err
	}

	parentStat, err := os.Stat(filepath.Dir(realPath))
	if err != nil {
		return false, err
	}

	// If the directory has a different device as parent, then it is a mountpoint.
	if stat.Sys().(*syscall.Stat_t).Dev != parentStat.Sys().(*syscall.Stat_t).Dev {
		return true, nil
	}

	return false, nil
}

func WaitUntilUnmounted(path string) error {
	return retry.Do(func() error {
		mounted, err := IsMountpoint(path)
		if err != nil {
			return err
		}

		if mounted {
			return errdefs.ErrDeviceBusy
		}

		return nil
	},
		retry.Attempts(20), // totally wait for 1 seconds, should be enough
		retry.LastErrorOnly(true),
		retry.Delay(50*time.Millisecond),
	)
}
