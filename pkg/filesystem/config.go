/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/pkg/errors"
)

type NewFSOpt func(d *Filesystem) error

func WithNydusImageBinaryPath(p string) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.nydusImageBinaryPath = p
		return nil
	}
}

func WithManager(pm *manager.Manager) NewFSOpt {
	return func(fs *Filesystem) error {
		if pm == nil {
			return errors.New("process manager cannot be nil")
		}

		fs.Manager = pm
		return nil
	}
}

func WithCacheManager(cm *cache.Manager) NewFSOpt {
	return func(fs *Filesystem) error {
		if cm == nil {
			return errors.New("cache manager cannot be nil")
		}

		fs.cacheMgr = cm
		return nil
	}
}

func WithVerifier(verifier *signature.Verifier) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.verifier = verifier
		return nil
	}
}

func WithRootMountpoint(mountpoint string) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.rootMountpoint = mountpoint
		return nil
	}
}

func WithEnableStargz(enable bool) NewFSOpt {
	return func(fs *Filesystem) error {
		if enable {
			fs.stargzResolver = stargz.NewResolver()
		}
		return nil
	}
}
