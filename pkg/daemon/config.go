/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"os"
	"path"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/internal/constant"
)

// Build runtime nydusd daemon object, which might be persisted later
func WithSocketDir(dir string) NewDaemonOpt {
	return func(d *Daemon) error {
		s := filepath.Join(dir, d.ID())
		// this may be failed, should handle that
		if err := os.MkdirAll(s, 0755); err != nil {
			return errors.Wrapf(err, "create socket dir %s", s)
		}
		d.States.APISocket = path.Join(s, "api.sock")
		return nil
	}
}

func WithRef(ref int32) NewDaemonOpt {
	return func(d *Daemon) error {
		d.ref = ref
		return nil
	}
}

func WithLogDir(dir string) NewDaemonOpt {
	return func(d *Daemon) error {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return errors.Wrapf(err, "create logging dir %s", dir)
		}
		d.States.LogDir = filepath.Join(dir, d.ID())
		return nil
	}
}

func WithLogToStdout(logToStdout bool) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.LogToStdout = logToStdout
		return nil
	}
}

func WithLogLevel(logLevel string) NewDaemonOpt {
	return func(d *Daemon) error {
		if logLevel == "" {
			d.States.LogLevel = constant.DefaultLogLevel
		} else {
			d.States.LogLevel = logLevel
		}
		return nil
	}
}

func WithLogRotationSize(logRotationSize int) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.LogRotationSize = logRotationSize
		return nil
	}
}

func WithConfigDir(dir string) NewDaemonOpt {
	return func(d *Daemon) error {
		s := filepath.Join(dir, d.ID())
		// this may be failed, should handle that
		if err := os.MkdirAll(s, 0755); err != nil {
			return errors.Wrapf(err, "failed to create config dir %s", s)
		}
		d.States.ConfigDir = s
		return nil
	}
}

func WithMountpoint(mountpoint string) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.Mountpoint = mountpoint
		return nil
	}
}

func WithNydusdThreadNum(nydusdThreadNum int) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.ThreadNum = nydusdThreadNum
		return nil
	}
}

func WithFsDriver(fsDriver string) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.FsDriver = fsDriver
		return nil
	}
}

func WithDaemonMode(daemonMode config.DaemonMode) NewDaemonOpt {
	return func(d *Daemon) error {
		d.States.DaemonMode = daemonMode
		return nil
	}
}
