/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/nydussdk"
	"github.com/containerd/nydus-snapshotter/pkg/nydussdk/model"
	"github.com/containerd/nydus-snapshotter/pkg/utils/erofs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
	"github.com/pkg/errors"
)

const (
	APISocketFileName   = "api.sock"
	SharedNydusDaemonID = "shared_daemon"
)

type NewDaemonOpt func(d *Daemon) error

type Daemon struct {
	ID               string
	SnapshotID       string
	ConfigDir        string
	SocketDir        string
	LogDir           string
	LogLevel         string
	LogToStdout      bool
	SnapshotDir      string
	Pid              int
	ImageID          string
	DaemonMode       string
	DaemonBackend    string
	APISock          *string
	RootMountPoint   *string
	CustomMountPoint *string
	nydusdThreadNum  int

	// Client will be rebuilt on Reconnect, skip marshal/unmarshal
	Client nydussdk.Interface `json:"-"`
	Once   *sync.Once         `json:"-"`
}

func (d *Daemon) SharedMountPoint() string {
	return filepath.Join(*d.RootMountPoint, d.SnapshotID, "fs")
}

func (d *Daemon) MountPoint() string {
	if d.RootMountPoint != nil {
		return filepath.Join("/", d.SnapshotID, "fs")
	}
	if d.CustomMountPoint != nil {
		return *d.CustomMountPoint
	}
	return filepath.Join(d.SnapshotDir, d.SnapshotID, "fs")
}

func (d *Daemon) OldMountPoint() string {
	return filepath.Join(d.SnapshotDir, d.SnapshotID, "fs")
}

func (d *Daemon) BootstrapFile() (string, error) {
	return GetBootstrapFile(d.SnapshotDir, d.SnapshotID)
}

func (d *Daemon) ConfigFile() string {
	return filepath.Join(d.ConfigDir, "config.json")
}

// NydusdThreadNum returns `nydusd-thread-num` for nydusd if set,
// otherwise will return the number of CPUs as default.
func (d *Daemon) NydusdThreadNum() string {
	if d.nydusdThreadNum > 0 {
		return strconv.Itoa(d.nydusdThreadNum)
	}
	// if nydusd-thread-num is not set, return empty string
	// to let manager don't set thread-num option.
	return ""
}

func (d *Daemon) GetAPISock() string {
	if d.APISock != nil {
		return *d.APISock
	}
	return filepath.Join(d.SocketDir, APISocketFileName)
}

func (d *Daemon) FscacheWorkDir() string {
	return filepath.Join(d.SnapshotDir, d.SnapshotID, "fs")
}

func (d *Daemon) LogFile() string {
	return filepath.Join(d.LogDir, "stderr.log")
}

func (d *Daemon) CheckStatus() (*model.DaemonInfo, error) {
	if err := d.ensureClient("check status"); err != nil {
		return nil, err
	}

	return d.Client.CheckStatus()
}

func (d *Daemon) WaitUntilReady() error {
	return retry.Do(func() error {
		info, err := d.CheckStatus()
		if err != nil {
			return errors.Wrap(err, "wait until daemon ready by checking status")
		}
		if !info.Running() {
			return fmt.Errorf("daemon %s is not ready: %v", d.ID, info)
		}
		return nil
	},
		retry.Attempts(3),
		retry.LastErrorOnly(true),
		retry.Delay(100*time.Millisecond),
	)
}

func (d *Daemon) SharedMount() error {
	if err := d.ensureClient("share mount"); err != nil {
		return err
	}
	if d.DaemonBackend == config.DaemonBackendFscache {
		if err := d.sharedErofsMount(); err != nil {
			return errors.Wrapf(err, "failed to erofs mount")
		}
		return nil
	}
	bootstrap, err := d.BootstrapFile()
	if err != nil {
		return err
	}
	return d.Client.SharedMount(d.MountPoint(), bootstrap, d.ConfigFile())
}

func (d *Daemon) SharedUmount() error {
	if err := d.ensureClient("share umount"); err != nil {
		return err
	}
	if d.DaemonBackend == config.DaemonBackendFscache {
		if err := d.sharedErofsUmount(); err != nil {
			return errors.Wrapf(err, "failed to erofs mount")
		}
		return nil
	}
	return d.Client.Umount(d.MountPoint())
}

func (d *Daemon) sharedErofsMount() error {
	if err := d.ensureClient("erofs mount"); err != nil {
		return err
	}

	if err := os.MkdirAll(d.FscacheWorkDir(), 0755); err != nil {
		return errors.Wrapf(err, "failed to create fscache work dir %s", d.FscacheWorkDir())
	}

	if err := d.Client.FscacheBindBlob(d.ConfigFile()); err != nil {
		return errors.Wrapf(err, "request to bind fscache blob")
	}

	mountPoint := d.SharedMountPoint()
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return errors.Wrapf(err, "failed to create mount dir %s", mountPoint)
	}

	bootstrapPath, err := d.BootstrapFile()
	if err != nil {
		return err
	}
	fscacheID := erofs.FscacheID(d.SnapshotID)

	if err := erofs.Mount(bootstrapPath, fscacheID, mountPoint); err != nil {
		return errors.Wrapf(err, "mount erofs")
	}

	return nil
}

func (d *Daemon) sharedErofsUmount() error {
	if err := d.ensureClient("erofs umount"); err != nil {
		return err
	}

	if err := d.Client.FscacheUnbindBlob(d.ConfigFile()); err != nil {
		return errors.Wrapf(err, "request to unbind fscache blob")
	}

	mountPoint := d.SharedMountPoint()
	if err := erofs.Umount(mountPoint); err != nil {
		return errors.Wrapf(err, "umount erofs")
	}

	return nil
}

func (d *Daemon) GetFsMetric(sharedDaemon bool, sid string) (*model.FsMetric, error) {
	if err := d.ensureClient("get metric"); err != nil {
		return nil, err
	}
	return d.Client.GetFsMetric(sharedDaemon, sid)
}

func (d *Daemon) IsMultipleDaemon() bool {
	return d.DaemonMode == config.DaemonModeMultiple
}

func (d *Daemon) IsSharedDaemon() bool {
	return d.DaemonMode == config.DaemonModeShared
}

func (d *Daemon) IsPrefetchDaemon() bool {
	return d.DaemonMode == config.DaemonModePrefetch
}

func (d *Daemon) initClient() error {
	client, err := nydussdk.NewNydusClient(d.GetAPISock())
	if err != nil {
		return errors.Wrap(err, "failed to create new nydus client")
	}
	d.Client = client
	return nil
}

func (d *Daemon) ensureClient(action string) error {
	var err error
	d.Once.Do(func() {
		if d.Client == nil {
			if ierr := d.initClient(); ierr != nil {
				err = errors.Wrapf(ierr, "failed to %s", action)
				d.Once = &sync.Once{}
			}
		}
	})
	if err == nil && d.Client == nil {
		return fmt.Errorf("failed to %s, client not initialized", action)
	}
	return err
}

func NewDaemon(opt ...NewDaemonOpt) (*Daemon, error) {
	d := &Daemon{Pid: 0}
	d.ID = newID()
	d.DaemonMode = config.DefaultDaemonMode
	d.Once = &sync.Once{}
	for _, o := range opt {
		err := o(d)
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

func GetBootstrapFile(dir, id string) (string, error) {
	// the meta file is stored to <snapshotid>/image/image.boot
	bootstrap := filepath.Join(dir, id, "fs", "image", "image.boot")
	_, err := os.Stat(bootstrap)
	if err == nil {
		return bootstrap, nil
	}
	if os.IsNotExist(err) {
		// for backward compatibility check meta file from legacy location
		bootstrap = filepath.Join(dir, id, "fs", "image.boot")
		_, err = os.Stat(bootstrap)
		if err == nil {
			return bootstrap, nil
		}
	}
	return "", errors.Wrap(err, fmt.Sprintf("failed to find bootstrap file for ID %s", id))
}
