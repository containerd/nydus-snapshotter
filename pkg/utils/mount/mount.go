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

	"github.com/containerd/log"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
)

type Interface interface {
	Umount(target string) error
}

type Mounter struct {
}

// These indirections are test seams so unit tests can stub out the real
// syscall / filesystem check without touching real mountpoints.
var (
	syscallUnmount = syscall.Unmount
	isMountpoint   = IsMountpoint
)

// isDisconnected reports whether err indicates a broken/stale mountpoint whose
// backing server (e.g. a dead nydusd behind a FUSE mount) is gone. Such a
// mountpoint still needs to be unmounted, so callers must not bail out on it.
func isDisconnected(err error) bool {
	return errors.Is(err, syscall.ENOTCONN) || errors.Is(err, syscall.ESTALE)
}

// unmountWithFallback tries a plain unmount first and, on failure, degrades to
// force then lazy detach so that busy or disconnected mountpoints are always
// torn down instead of being left behind.
func unmountWithFallback(target string) error {
	err := syscallUnmount(target, 0)
	if err == nil || errors.Is(err, syscall.EINVAL) {
		// EINVAL means the target is not a mountpoint (already unmounted).
		return nil
	}

	// umountForce aborts in-flight requests, which is what a disconnected FUSE
	// mount needs; try it first.
	if ferr := syscallUnmount(target, umountForce); ferr == nil {
		log.L.Warnf("force umount %s after plain umount failed: %v", target, err)
		return nil
	}

	// umountDetach (lazy) detaches from the namespace even while busy; last resort.
	if lerr := syscallUnmount(target, umountDetach); lerr != nil {
		return errors.Wrapf(lerr, "lazy umount %s (plain umount error: %v)", target, err)
	}
	log.L.Warnf("lazy-detached %s after plain umount failed: %v", target, err)
	return nil
}

func (m *Mounter) Umount(target string) error {
	mounted, err := isMountpoint(target)
	if err != nil {
		// A disconnected/stale mountpoint fails IsMountpoint (stat returns
		// ENOTCONN) but still needs unmounting; proceed instead of bailing out.
		if !isDisconnected(err) {
			return err
		}
		mounted = true
	}

	if !mounted {
		return nil
	}

	return unmountWithFallback(target)
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
		mounted, err := isMountpoint(path)
		if err != nil {
			if isDisconnected(err) {
				return nil
			}
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
