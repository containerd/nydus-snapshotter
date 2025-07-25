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
	"github.com/containerd/nydus-snapshotter/pkg/referrer"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/tarfs"
	"github.com/pkg/errors"
)

type NewFSOpt func(d *Filesystem) error

func WithNydusdBinaryPath(p string) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.nydusdBinaryPath = p
		return nil
	}
}

func WithManagers(managers []*manager.Manager) NewFSOpt {
	return func(fs *Filesystem) error {
		if fs.enabledManagers == nil {
			fs.enabledManagers = map[string]*manager.Manager{}
		}
		for _, pm := range managers {
			fs.enabledManagers[pm.FsDriver] = pm
		}
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

func WithReferrerManager(rm *referrer.Manager) NewFSOpt {
	return func(fs *Filesystem) error {
		if rm == nil {
			return errors.New("referrer manager cannot be nil")
		}

		fs.referrerMgr = rm
		return nil
	}
}

func WithTarfsManager(tm *tarfs.Manager) NewFSOpt {
	return func(fs *Filesystem) error {
		if tm == nil {
			return errors.New("tarfs manager cannot be nil")
		}
		fs.tarfsMgr = tm
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
