/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"os"
	"strings"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/process"
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

func WithNydusdBinaryPath(p string, daemonMode string) NewFSOpt {
	return func(d *Filesystem) error {
		if daemonMode != config.DaemonModeNone && p == "" {
			return errors.New("nydusd binary path is required")
		}
		d.nydusdBinaryPath = p
		return nil
	}
}

func WithProcessManager(pm *process.Manager) NewFSOpt {
	return func(d *Filesystem) error {
		if pm == nil {
			return errors.New("process manager cannot be nil")
		}

		d.manager = pm
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

func WithDaemonConfig(cfg config.DaemonConfig) NewFSOpt {
	return func(d *Filesystem) error {
		if (config.DaemonConfig{}) == cfg {
			return errors.New("daemon config is empty")
		}
		d.daemonCfg = cfg
		return nil
	}
}

func WithVPCRegistry(vpcRegistry bool) NewFSOpt {
	return func(d *Filesystem) error {
		d.vpcRegistry = vpcRegistry
		return nil
	}
}

func WithDaemonMode(daemonMode string) NewFSOpt {
	return func(d *Filesystem) error {
		mode := strings.ToLower(daemonMode)
		switch mode {
		case config.DaemonModeNone:
			d.mode = NoneInstance
		case config.DaemonModeShared:
			d.mode = SharedInstance
		case config.DaemonModePrefetch:
			d.mode = PrefetchInstance
		case config.DaemonModeMultiple:
			fallthrough
		default:
			d.mode = MultiInstance
		}
		return nil
	}
}

func WithDaemonBackend(daemonBackend string) NewFSOpt {
	return func(d *Filesystem) error {
		switch daemonBackend {
		case config.DaemonBackendFscache:
			d.daemonBackend = config.DaemonBackendFscache
		default:
			d.daemonBackend = config.DaemonBackendFusedev
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

func WithImageMode(cfg config.DaemonConfig) NewFSOpt {
	return func(d *Filesystem) error {
		if cfg.Device.Backend.BackendType == "localfs" &&
			len(cfg.Device.Backend.Config.Dir) != 0 {
			d.imageMode = PreLoad
		}
		return nil
	}
}
