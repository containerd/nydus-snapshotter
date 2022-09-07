/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/erofs"
	"github.com/containerd/nydus-snapshotter/pkg/utils/mount"
	"github.com/containerd/nydus-snapshotter/pkg/utils/retry"
	"github.com/pkg/errors"
)

const (
	APISocketFileName   = "api.sock"
	SharedNydusDaemonID = "shared_daemon"
)

type NewDaemonOpt func(d *Daemon) error

// TODO: Record queried nydusd state
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
	DaemonMode       config.DaemonMode
	FsDriver         string
	APISock          *string
	RootMountPoint   *string
	CustomMountPoint *string
	nydusdThreadNum  int

	// client will be rebuilt on Reconnect, skip marshal/unmarshal
	client NydusdClient `json:"-"`
	Once   *sync.Once   `json:"-"`
	// It should only be used to distinguish daemons that needs to be started when restarting nydus-snapshotter
	Connected bool       `json:"-"`
	mu        sync.Mutex `json:"-"`
	domainID  string     `json:"-"`
}

func (d *Daemon) Lock() {
	d.mu.Lock()
}

func (d *Daemon) Unlock() {
	d.mu.Unlock()
}

// Mountpoint for nydusd within single kernel mountpoint(FUSE mount). Each mountpoint
// is create by API based pseudo mount. `RootMountPoint` is real mountpoint
// where to perform the kernel mount.
// Nydusd API based mountpoint must start with "/", otherwise nydusd API server returns error.
func (d *Daemon) SharedMountPoint() string {
	return filepath.Join("/", d.SnapshotID)
}

// This is generally used for overlayfs lower dir path.
func (d *Daemon) SharedAbsMountPoint() string {
	return filepath.Join(*d.RootMountPoint, d.SharedMountPoint())
}

// Mountpoint of per-image nydusd/rafs. It is a kernel mountpoint for each
// nydus meta layer. Each meta layer is associated with a nydusd.
func (d *Daemon) MountPoint() string {
	if d.CustomMountPoint != nil {
		return *d.CustomMountPoint
	}

	return filepath.Join(d.SnapshotDir, d.SnapshotID, "mnt")
}

func (d *Daemon) HostMountPoint() (mnt string) {
	// Identify a shared nydusd for multiple rafs instances.
	if d.ID == SharedNydusDaemonID {
		mnt = *d.RootMountPoint
	} else {
		mnt = d.MountPoint()
	}

	return
}

// Keep this for backwards compatibility
func (d *Daemon) OldMountPoint() string {
	return filepath.Join(d.SnapshotDir, d.SnapshotID, "fs")
}

func (d *Daemon) BootstrapFile() (string, error) {
	return GetBootstrapFile(d.SnapshotDir, d.SnapshotID)
}

// Each nydusd daemon has a copy of configuration json file.
func (d *Daemon) ConfigFile() string {
	return filepath.Join(d.ConfigDir, "config.json")
}

// NydusdThreadNum returns how many working threads are needed of a single nydusd
func (d *Daemon) NydusdThreadNum() int {
	return d.nydusdThreadNum
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
	return filepath.Join(d.LogDir, "nydusd.log")
}

// Nydusd daemon current working state by requesting to nydusd:
// 1. INIT
// 2. READY: All needed resources are ready.
// 3. RUNNING
func (d *Daemon) State() (types.DaemonState, error) {
	if err := d.ensureClient("getting daemon state"); err != nil {
		return types.DaemonStateUnknown, err
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	info, err := d.client.GetDaemonInfo()
	if err != nil {
		return types.DaemonStateUnknown, err
	}

	return info.DaemonState(), nil
}

// Waits for some time until daemon reaches the expected state.
// For example:
//  1. INIT
//  2. READY
//  3. RUNNING
func (d *Daemon) WaitUntilState(expected types.DaemonState) error {
	return retry.Do(func() error {
		state, err := d.State()
		if err != nil {
			return errors.Wrapf(err, "wait until daemon is %s", expected)
		}

		if state != expected {
			return errors.Errorf("daemon %s is not %s yet, current state %s",
				d.ID, expected, state)
		}

		return nil
	},
		retry.Attempts(20), // totally wait for 2 seconds, should be enough
		retry.LastErrorOnly(true),
		retry.Delay(100*time.Millisecond),
	)
}

func (d *Daemon) SharedMount() error {
	if err := d.ensureClient("share mount"); err != nil {
		return err
	}
	if d.FsDriver == config.FsDriverFscache {
		if err := d.sharedErofsMount(); err != nil {
			return errors.Wrapf(err, "failed to erofs mount")
		}
		return nil
	}
	bootstrap, err := d.BootstrapFile()
	if err != nil {
		return err
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	return d.client.Mount(d.SharedMountPoint(), bootstrap, d.ConfigFile())
}

func (d *Daemon) SharedUmount() error {
	if err := d.ensureClient("share umount"); err != nil {
		return err
	}

	if d.FsDriver == config.FsDriverFscache {
		if err := d.sharedErofsUmount(); err != nil {
			return errors.Wrapf(err, "failed to erofs mount")
		}
		return nil
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	return d.client.Umount(d.SharedMountPoint())
}

func (d *Daemon) sharedErofsMount() error {
	if err := d.ensureClient("erofs mount"); err != nil {
		return err
	}

	if err := os.MkdirAll(d.FscacheWorkDir(), 0755); err != nil {
		return errors.Wrapf(err, "failed to create fscache work dir %s", d.FscacheWorkDir())
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	if err := d.client.BindBlob(d.ConfigFile()); err != nil {
		return errors.Wrapf(err, "request to bind fscache blob")
	}

	mountPoint := d.MountPoint()
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return errors.Wrapf(err, "failed to create mount dir %s", mountPoint)
	}

	bootstrapPath, err := d.BootstrapFile()
	if err != nil {
		return err
	}
	fscacheID := erofs.FscacheID(d.SnapshotID)

	if err := erofs.Mount(bootstrapPath, d.domainID, fscacheID, mountPoint); err != nil {
		if !errdefs.IsErofsMounted(err) {
			return errors.Wrapf(err, "mount erofs to %s", mountPoint)
		}
		// When snapshotter exits (either normally or abnormally), it will not have a
		// chance to umount erofs mountpoint, so if snapshotter resumes running and mount
		// again (by a new request to create container), it will need to ignore the mount
		// error `device or resource busy`.
		log.L.Warnf("erofs mountpoint %s has been mounted", mountPoint)
	}

	return nil
}

func (d *Daemon) sharedErofsUmount() error {
	if err := d.ensureClient("erofs umount"); err != nil {
		return err
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	if err := d.client.UnbindBlob(d.ConfigFile()); err != nil {
		return errors.Wrapf(err, "request to unbind fscache blob")
	}

	mountPoint := d.MountPoint()
	if err := erofs.Umount(mountPoint); err != nil {
		return errors.Wrapf(err, "umount erofs")
	}

	return nil
}

func (d *Daemon) SendStates() error {
	if err := d.ensureClient("send states"); err != nil {
		return err
	}

	if err := d.client.SendFd(); err != nil {
		return errors.Wrap(err, "request to send states")
	}

	return nil
}

func (d *Daemon) TakeOver() error {
	if err := d.ensureClient("takeover"); err != nil {
		return err
	}

	if err := d.client.TakeOver(); err != nil {
		return errors.Wrap(err, "request to take over")
	}

	return nil
}

func (d *Daemon) Start() error {
	if err := d.ensureClient("start service"); err != nil {
		return err
	}

	if err := d.client.Start(); err != nil {
		return errors.Wrap(err, "request to start service")
	}

	return nil
}

func (d *Daemon) GetFsMetrics(sharedDaemon bool, sid string) (*types.FsMetrics, error) {
	if err := d.ensureClient("get metrics"); err != nil {
		return nil, err
	}

	// Protect daemon client when it's being reset.
	d.Lock()
	defer d.Unlock()

	return d.client.GetFsMetrics(sharedDaemon, sid)
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
	client, err := NewNydusClient(d.GetAPISock())
	if err != nil {
		return errors.Wrap(err, "failed to create new nydus client")
	}

	d.client = client
	return nil
}

func (d *Daemon) ResetClient() {
	d.Lock()
	d.client = nil
	d.Once = &sync.Once{}
	d.Unlock()
}

func (d *Daemon) ensureClient(situation string) (err error) {
	d.Lock()
	defer d.Unlock()

	d.Once.Do(func() {
		if d.client == nil {
			if ierr := d.initClient(); ierr != nil {
				err = errors.Wrapf(ierr, "failed to %s", situation)
				d.Once = &sync.Once{}
			}
		}
	})

	if err == nil && d.client == nil {
		return errors.Errorf("failed to %s, client not initialized", situation)
	}

	return
}

func (d *Daemon) Terminate() error {
	// if we found pid here, we need to kill and wait process to exit, Pid=0 means somehow we lost
	// the daemon pid, so that we can't kill the process, just roughly umount the mountpoint

	d.Lock()
	defer d.Unlock()

	if d.Pid > 0 {
		p, err := os.FindProcess(d.Pid)
		if err != nil {
			return errors.Wrapf(err, "find process %d", d.Pid)
		}
		if err = p.Signal(syscall.SIGTERM); err != nil {
			return errors.Wrapf(err, "send SIGTERM signal to process %d", d.Pid)
		}
	}

	return nil
}

func (d *Daemon) Wait() error {
	// if we found pid here, we need to kill and wait process to exit, Pid=0 means somehow we lost
	// the daemon pid, so that we can't kill the process, just roughly umount the mountpoint

	d.Lock()
	defer d.Unlock()

	if d.Pid > 0 {
		p, err := os.FindProcess(d.Pid)
		if err != nil {
			return errors.Wrapf(err, "find process %d", d.Pid)
		}

		// if nydus-snapshotter restarts, it will break the relationship between nydusd and
		// nydus-snapshotter, p.Wait() will return err, so here should exclude this case
		if _, err = p.Wait(); err != nil && !errors.Is(err, syscall.ECHILD) {
			log.L.Errorf("failed to process wait, %v", err)
		}
	}

	return nil
}

func (d *Daemon) ClearVestige() {
	mounter := mount.Mounter{}
	// This is best effort. So no need to handle its error.
	log.L.Infof("Umounting %s when clear vestige", d.HostMountPoint())

	if err := mounter.Umount(d.HostMountPoint()); err != nil {
		log.L.Warnf("Can't umount %s, %v", *d.RootMountPoint, err)
	}
	// Nydusd judges if it should enter failover phrase by checking
	// if unix socket is existed and it can't be connected.
	if err := os.Remove(d.GetAPISock()); err != nil {
		log.L.Warnf("Can't delete residual unix socket %s, %v", d.GetAPISock(), err)
	}

	// Let't transport builder wait for nydusd startup again until it sees the created socket file.
	d.ResetClient()
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
	// meta files are stored at <snapshot_id>/fs/image/image.boot
	bootstrap := filepath.Join(dir, id, "fs", "image", "image.boot")
	_, err := os.Stat(bootstrap)
	if err == nil {
		return bootstrap, nil
	}
	if os.IsNotExist(err) {
		// check legacy location for backward compatibility
		bootstrap = filepath.Join(dir, id, "fs", "image.boot")
		_, err = os.Stat(bootstrap)
		if err == nil {
			return bootstrap, nil
		}
	}
	return "", errors.Wrap(err, fmt.Sprintf("failed to find bootstrap file for ID %s", id))
}
