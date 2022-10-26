/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"os"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/fs/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/pkg/errors"
)

type NewFSOpt func(d *Filesystem) error

func WithMeta(root string) NewFSOpt {
	return func(d *Filesystem) error {
		if root == "" {
			return errors.New("rootDir is required")
		}
		d.FileSystemMeta = meta.FileSystemMeta{
			RootDir: root,
		}
		return nil
	}
}

func WithNydusdBinaryPath(p string, daemonMode config.DaemonMode) NewFSOpt {
	return func(d *Filesystem) error {
		if daemonMode != config.DaemonModeNone && p == "" {
			return errors.New("nydusd binary path is required")
		}
		d.nydusdBinaryPath = p
		return nil
	}
}

func WithNydusImageBinaryPath(p string) NewFSOpt {
	return func(d *Filesystem) error {
		d.nydusImageBinaryPath = p
		return nil
	}
}

func WithManager(pm *manager.Manager) NewFSOpt {
	return func(d *Filesystem) error {
		if pm == nil {
			return errors.New("process manager cannot be nil")
		}

		d.Manager = pm
		return nil
	}
}

func WithCacheManager(cm *cache.Manager) NewFSOpt {
	return func(d *Filesystem) error {
		if cm == nil {
			return errors.New("cache manager cannot be nil")
		}

		d.cacheMgr = cm
		return nil
	}
}

func WithVerifier(verifier *signature.Verifier) NewFSOpt {
	return func(d *Filesystem) error {
		d.verifier = verifier
		return nil
	}
}

func WithVPCRegistry(vpcRegistry bool) NewFSOpt {
	return func(d *Filesystem) error {
		d.vpcRegistry = vpcRegistry
		return nil
	}
}

func WithDaemonMode(daemonMode config.DaemonMode) NewFSOpt {
	return func(d *Filesystem) error {
		d.mode = daemonMode
		return nil
	}
}

func WithFsDriver(fsDriver string) NewFSOpt {
	return func(d *Filesystem) error {
		switch fsDriver {
		case config.FsDriverFscache:
			d.fsDriver = config.FsDriverFscache
		default:
			d.fsDriver = config.FsDriverFusedev
		}
		return nil
	}
}

func WithLogLevel(logLevel string) NewFSOpt {
	return func(d *Filesystem) error {
		if logLevel == "" {
			d.logLevel = config.DefaultLogLevel
		} else {
			d.logLevel = logLevel
		}
		return nil
	}
}

func WithLogDir(dir string) NewFSOpt {
	return func(d *Filesystem) error {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return errors.Errorf("failed to create logDir %s: %v", dir, err)
		}
		d.logDir = dir
		return nil
	}
}

func WithLogToStdout(logToStdout bool) NewFSOpt {
	return func(d *Filesystem) error {
		d.logToStdout = logToStdout
		return nil
	}
}

func WithNydusdThreadNum(nydusdThreadNum int) NewFSOpt {
	return func(d *Filesystem) error {
		d.nydusdThreadNum = nydusdThreadNum
		return nil
	}
}

func WithEnableStargz(enable bool) NewFSOpt {
	return func(d *Filesystem) error {
		if enable {
			d.stargzResolver = stargz.NewResolver()
		}
		return nil
	}
}
