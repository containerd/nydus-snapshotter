/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"os"

	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
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

func WithVPCRegistry(vpcRegistry bool) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.vpcRegistry = vpcRegistry
		return nil
	}
}

func WithDaemonMode(daemonMode command.DaemonMode) NewFSOpt {
	return func(fs *Filesystem) error {
		fs.mode = daemonMode
		return nil
	}
}

func WithFsDriver(fsDriver string) NewFSOpt {
	return func(fs *Filesystem) error {
		switch fsDriver {
		case config.FsDriverFscache:
			fs.fsDriver = config.FsDriverFscache
		default:
			fs.fsDriver = config.FsDriverFusedev
		}
		return nil
	}
}

func WithLogLevel(logLevel string) NewFSOpt {
	return func(fs *Filesystem) error {
		if logLevel == "" {
			fs.logLevel = config.DefaultLogLevel
		} else {
			fs.logLevel = logLevel
		}
		return nil
	}
}

func WithLogDir(dir string) NewFSOpt {
	return func(fs *Filesystem) error {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return errors.Errorf("failed to create logDir %s: %v", dir, err)
		}
		fs.logDir = dir
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
	return func(fs *Filesystem) error {
		fs.nydusdThreadNum = nydusdThreadNum
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
