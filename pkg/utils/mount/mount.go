/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package mount

import (
	"fmt"
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

// These indirections are test seams so unit tests can stub out the real
// syscall / filesystem check without touching real mountpoints.
var (
	syscallUnmount = syscall.Unmount
	isMountpoint   = IsMountpoint
)

var (
	// ErrUnmountedUnclean reports that the target was only removed from the mount
	// namespace by escalating to a forced or lazy unmount. The path is free to be
	// mounted over again, but the old mount is not necessarily gone: a lazy unmount
	// keeps it alive until the last reference drops, and a forced unmount aborts
	// in-flight requests so anything still using it observes I/O errors. Callers
	// must not treat this as a clean teardown and must not assume the backing
	// resources are safe to reclaim.
	ErrUnmountedUnclean = errors.New("unmounted but not cleanly")

	// ErrMountpointDisconnected reports that the path is still mounted but its
	// backing server (e.g. a dead nydusd behind a FUSE mount) is gone. Nothing has
	// been unmounted; the mountpoint still needs to be torn down.
	ErrMountpointDisconnected = errors.New("mountpoint disconnected")
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
//
// Only a plain unmount is a clean teardown. When the fallbacks are what freed the
// path, the returned error wraps ErrUnmountedUnclean so the caller can tell the
// difference; it is not reported as success.
func unmountWithFallback(target string) error {
	err := syscallUnmount(target, 0)
	if err == nil || errors.Is(err, syscall.EINVAL) {
		// EINVAL means the target is not a mountpoint (already unmounted).
		return nil
	}

	// umountForce aborts in-flight requests, which is what a disconnected FUSE
	// mount needs; try it first.
	if ferr := syscallUnmount(target, umountForce); ferr == nil {
		return errors.Wrapf(ErrUnmountedUnclean, "force umount %s (plain umount error: %v)", target, err)
	}

	// umountDetach (lazy) detaches from the namespace even while busy; last resort.
	if lerr := syscallUnmount(target, umountDetach); lerr != nil {
		return errors.Wrapf(lerr, "lazy umount %s (plain umount error: %v)", target, err)
	}
	return errors.Wrapf(ErrUnmountedUnclean, "lazy umount %s (plain umount error: %v)", target, err)
}

// Umount tears down target. A path that is not a mountpoint returns nil, i.e.
// unmounting is idempotent. A non-nil error wrapping ErrUnmountedUnclean means the
// path was freed by a forced or lazy unmount rather than a clean one.
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

// WaitUntilUnmounted waits for path to stop being a mountpoint. A disconnected
// mountpoint is still mounted, so it is reported as ErrMountpointDisconnected
// rather than as a successful unmount; retrying it is pointless because the
// backing server never comes back, so the wait is aborted immediately.
func WaitUntilUnmounted(path string) error {
	return retry.Do(func() error {
		mounted, err := isMountpoint(path)
		if err != nil {
			if isDisconnected(err) {
				return retry.Unrecoverable(
					fmt.Errorf("%w: %s is still mounted: %w", ErrMountpointDisconnected, path, err))
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
