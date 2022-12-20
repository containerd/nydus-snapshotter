/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Abstraction layer of underlying file systems. The file system could be mounted by one
// or more nydusd daemons. fs package hides the details

package filesystem

import (
	"context"
	"os"
	"path"

	"github.com/mohae/deepcopy"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"

<<<<<<< HEAD:pkg/filesystem/fs/fs.go
	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/internal/containerd-nydus-grpc/command"
||||||| dba3aaf:pkg/filesystem/fs/fs.go
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
=======
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
>>>>>>> 84c74391eeb49083c9452164758d17d38f07bfa6:pkg/filesystem/fs.go
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
)

// RafsV6 layout: 1k + SuperBlock(128) + SuperBlockExtended(256)
// RafsV5 layout: 8K superblock
// So we only need to read the MaxSuperBlockSize size to include both v5 and v6 superblocks
const MaxSuperBlockSize = 8 * 1024
const (
	BootstrapFile       string = "image/image.boot"
	LegacyBootstrapFile string = "image.boot"
	DummyMountpoint     string = "/dummy"
)

type Filesystem struct {
	// Managing all daemons serving filesystem.
	Manager              *manager.Manager
	cacheMgr             *cache.Manager
	sharedDaemon         *daemon.Daemon
	stargzResolver       *stargz.Resolver
	verifier             *signature.Verifier
	nydusImageBinaryPath string
	rootMountpoint       string
}

func (fs *Filesystem) tryRetainSharedDaemon(d *daemon.Daemon) {
	// FsDriver can be changed between two startups.
	if d.HostMountpoint() == fs.rootMountpoint || config.GetFsDriver() == config.FsDriverFscache {
		fs.sharedDaemon = d
		d.IncRef()
	}
}

// NewFileSystem initialize Filesystem instance
// It does mount image layers by starting nydusd doing FUSE mount or not.
func NewFileSystem(ctx context.Context, opt ...NewFSOpt) (*Filesystem, error) {
	var fs Filesystem
	for _, o := range opt {
		err := o(&fs)
		if err != nil {
			return nil, err
		}
	}

	// Try to reconnect to running daemons
	recoveringDaemons, liveDaemons, err := fs.Manager.Recover(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to reconnect daemons")
	}

	// Try to bring up the shared daemon early.
	// With found recovering daemons, it must be the case that snapshotter is being restarted.
	if config.GetDaemonMode() == command.DaemonModeShared && len(liveDaemons) == 0 && len(recoveringDaemons) == 0 {
		// Situations that shared daemon is not found:
		//   1. The first time this nydus-snapshotter runs
		//   2. Daemon record is wrongly deleted from DB. Above reconnecting already gathers
		//		all daemons but still not found shared daemon. The best workaround is to start
		//		a new nydusd for it.
		// TODO: We still need to consider shared daemon the time sequence of initializing daemon,
		// start daemon commit its state to DB and retrieving its state.
		log.L.Infof("initializing the shared nydus daemon")
		if err := fs.initSharedDaemon(); err != nil {
			return nil, errors.Wrap(err, "start shared nydusd daemon")
		}
	}

	// Try to bring all persisted and stopped nydusd up and remount Rafs
	for _, d := range recoveringDaemons {
		if err := fs.Manager.StartDaemon(d); err != nil {
			return nil, errors.Wrapf(err, "start daemon %s", d.ID())
		}
		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			return nil, errors.Wrapf(err, "wait for daemon %s", d.ID())
		}
		if err := d.RecoveredMountInstances(); err != nil {
			return nil, errors.Wrapf(err, "recover mounts for daemon %s", d.ID())
		}

		// Found shared daemon
		// Fscache userspace daemon has no host mountpoint.
		fs.tryRetainSharedDaemon(d)

	}

	for _, d := range liveDaemons {
		// Found shared daemon
		fs.tryRetainSharedDaemon(d)
	}

	return &fs, nil
}

// The globally shared daemon must be running before using it
// So we don't check if it is none here
// NIL shared damon means no shared daemon is ever needed and required.
func (fs *Filesystem) getSharedDaemon() *daemon.Daemon {
	return fs.sharedDaemon
}

func (fs *Filesystem) decideDaemonMountpoint(rafs *daemon.Rafs) (string, error) {
	var m string
	if fs.Manager.IsSharedDaemon() {
		if config.GetFsDriver() == config.FsDriverFscache {
			return "", nil
		}
		m = fs.rootMountpoint
	} else {
		m = path.Join(rafs.GetSnapshotDir(), "mnt")
	}

	if err := os.MkdirAll(m, 0755); err != nil {
		return "", errors.Wrapf(err, "create directory %s", m)
	}

	return m, nil
}

// WaitUntilReady wait until daemon ready by snapshotID, it will wait until nydus domain socket established
// and the status of nydusd daemon must be ready
func (fs *Filesystem) WaitUntilReady(snapshotID string) error {
	// If NoneDaemon mode, there's no need to wait for daemon ready
	if !fs.DaemonBacked() {
		return nil
	}

	instance := daemon.RafsSet.Get(snapshotID)
	if instance == nil {
		return errors.Wrapf(errdefs.ErrNotFound, "no instance %s", snapshotID)
	}

	d := fs.Manager.GetByDaemonID(instance.DaemonID)
	if d == nil {
		return errors.Wrapf(errdefs.ErrNotFound, "snapshot id %s daemon id %s", snapshotID, instance.DaemonID)
	}

	if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
		return err
	}

	log.L.Infof("Nydus remote snapshot %s is ready", snapshotID)

	return nil
}

// Mount will be called when containerd snapshotter prepare remote snapshotter
// this method will fork nydus daemon and manage it in the internal store, and indexed by snapshotID
// It must set up all necessary resources during Mount procedure and revoke any step if necessary.
func (fs *Filesystem) Mount(snapshotID string, labels map[string]string) (err error) {
	// If NoneDaemon mode, we don't mount nydus on host
	if !fs.DaemonBacked() {
		return nil
	}

	imageID, ok := labels[label.CRIImageRef]
	if !ok {
		return errors.Errorf("failed to find image ref of snapshot %s, labels %v",
			snapshotID, labels)
	}

	r := daemon.RafsSet.Get(snapshotID)
	if r != nil {
		// Containerd can handle this error?
		return nil
	}

	rafs, err := daemon.NewRafs(snapshotID, imageID)
	if err != nil {
		return errors.Wrapf(err, "create rafs instance %s", snapshotID)
	}

	defer func() {
		if err != nil {
			daemon.RafsSet.Remove(snapshotID)
		}
	}()

	var d *daemon.Daemon
	if fs.getSharedDaemon() != nil {
		d = fs.getSharedDaemon()
		d.AddInstance(rafs)
	} else {
		mp, err := fs.decideDaemonMountpoint(rafs)
		if err != nil {
			return err
		}
		d, err = fs.createDaemon(mp, 0)
		// if daemon already exists for snapshotID, just return
		if err != nil {
			if errdefs.IsAlreadyExists(err) {
				return nil
			}
			return err
		}
		d.AddInstance(rafs)
	}

	bootstrap, err := rafs.BootstrapFile()
	if err != nil {
		return errors.Wrapf(err, "find bootstrap file of daemon %s snapshot %s", d.ID(), snapshotID)
	}

	workDir := rafs.FscacheWorkDir()
	// Nydusd uses cache manager's directory to store blob caches. So cache
	// manager knows where to find those blobs.
	cacheDir := fs.cacheMgr.CacheDir()

	params := map[string]string{
		config.Bootstrap: bootstrap,
		// Fscache driver stores blob cache bitmap and blob header files here
		config.WorkDir:  workDir,
		config.CacheDir: cacheDir}

	cfg := deepcopy.Copy(fs.Manager.DaemonConfig).(config.DaemonConfigInterface)
	err = config.SupplementDaemonConfig(cfg, imageID, snapshotID, false, labels, params)
	if err != nil {
		return errors.Wrap(err, "supplement configuration")
	}

	// TODO: How to manage rafs configurations on-disk? separated json config file or DB record?
	// In order to recover erofs mount, the configuration file has to be persisted.
	var configSubDir string
	if fs.getSharedDaemon() == nil {
		// Associate daemon config object when creating a new daemon object to avoid
		// reading disk file again and again.
		// For shared daemon, each rafs instance has its own configuration, so we don't
		// attach a config interface to daemon in this case.
		d.Config = cfg
	} else {
		configSubDir = snapshotID
	}

	err = cfg.DumpFile(d.ConfigFile(configSubDir))
	if err != nil {
		if errors.Is(err, errdefs.ErrAlreadyExists) {
			log.L.Debugf("Configuration file %s already exits", d.ConfigFile(configSubDir))
		} else {
			return errors.Wrap(err, "dump daemon configuration file")
		}
	}

	// if publicKey is not empty we should verify bootstrap file of image
	err = fs.verifier.Verify(labels, bootstrap)
	if err != nil {
		return errors.Wrapf(err, "verify signature of daemon %s", d.ID())
	}

	err = fs.mount(d, rafs)
	if err != nil {
		return errors.Wrapf(err, "mount file system by daemon %s, snapshot %s", d.ID(), snapshotID)
	}

	// Persist it after associate instance after all the states are calculated.
	if err := fs.Manager.NewInstance(rafs); err != nil {
		return errors.Wrapf(err, "create instance %s", snapshotID)
	}

	return nil
}

func (fs *Filesystem) Umount(ctx context.Context, snapshotID string) error {
	instance := daemon.RafsSet.Get(snapshotID)
	if instance == nil {
		log.L.Debugf("Not a rafs instance. ID %s", snapshotID)
		return nil
	}

	daemon := fs.Manager.GetByDaemonID(instance.DaemonID)
	if daemon == nil {
		log.L.Infof("snapshot %s does not correspond to a nydusd", snapshotID)
		return nil
	}

	log.L.Infof("umount snapshot %s, daemon ID %s", snapshotID, daemon.ID())

	daemon.RemoveInstance(snapshotID)
	if err := daemon.UmountInstance(instance); err != nil {
		return errors.Wrapf(err, "umount instance %s", snapshotID)
	}

	if err := fs.Manager.RemoveInstance(snapshotID); err != nil {
		return errors.Wrapf(err, "remove instance %s", snapshotID)
	}

	// Once daemon's reference reaches 0, destroy the whole daemon
	if daemon.GetRef() == 0 {
		if err := fs.Manager.DestroyDaemon(daemon); err != nil {
			return errors.Wrapf(err, "destroy daemon %s", daemon.ID())
		}
	}

	return nil
}

// How much space the layer/blob cache filesystem is occupying
// The blob digest mush have `sha256:` prefixed, otherwise, throw errors.
func (fs *Filesystem) CacheUsage(ctx context.Context, blobDigest string) (snapshots.Usage, error) {
	digest := digest.Digest(blobDigest)
	if err := digest.Validate(); err != nil {
		return snapshots.Usage{}, errors.Wrapf(err, "invalid blob digest from label %s", label.CRILayerDigest)
	}
	blobID := digest.Hex()
	return fs.cacheMgr.CacheUsage(ctx, blobID)
}

func (fs *Filesystem) RemoveCache(blobDigest string) error {
	digest := digest.Digest(blobDigest)
	if err := digest.Validate(); err != nil {
		return errors.Wrapf(err, "invalid blob digest from label %s", label.CRILayerDigest)
	}
	blobID := digest.Hex()
	return fs.cacheMgr.RemoveBlobCache(blobID)
}

// Try to stop all the running daemons if they are not referenced by any snapshots
// Clean up resources along with the daemons.
func (fs *Filesystem) Teardown(ctx context.Context) error {
	for _, d := range fs.Manager.ListDaemons() {
		for _, instance := range d.Instances.List() {
			err := fs.Umount(ctx, instance.SnapshotID)
			if err != nil {
				log.L.Errorf("Failed to umount snapshot %s, %s", instance.SnapshotID, err)
			}
		}
	}

	return nil
}

func (fs *Filesystem) MountPoint(snapshotID string) (string, error) {
	if !fs.DaemonBacked() {
		// For NoneDaemon mode, return a dummy mountpoint which is very likely not
		// existed on host. NoneDaemon mode does not start nydusd, so NO fuse mount is
		// ever performed. Only mount option carries meaningful info to containerd and
		// finally passes to shim.
		return DummyMountpoint, nil
	}

	rafs := daemon.RafsSet.Get(snapshotID)
	return rafs.GetMountpoint(), nil
}

func (fs *Filesystem) BootstrapFile(id string) (string, error) {
	instance := daemon.RafsSet.Get(id)
	return instance.BootstrapFile()
}

// daemon mountpoint to rafs mountpoint
// calculate rafs mountpoint for snapshots mount slice.
func (fs *Filesystem) mount(d *daemon.Daemon, r *daemon.Rafs) error {
	if config.GetDaemonMode() == command.DaemonModeShared {
		if config.GetFsDriver() == config.FsDriverFusedev {
			r.SetMountpoint(path.Join(d.HostMountpoint(), r.SnapshotID))
		} else {
			r.SetMountpoint(path.Join(r.GetSnapshotDir(), "mnt"))
		}

		if err := d.SharedMount(r); err != nil {
			return errors.Wrapf(err, "failed to mount")
		}
	} else {
		err := fs.Manager.StartDaemon(d)
		if err != nil {
			return err
		}
		r.SetMountpoint(path.Join(d.HostMountpoint()))
		return errors.Wrapf(err, "start daemon")
	}

	return nil
}

// 1. Create a daemon instance
// 2. Build command line
// 3. Start daemon
func (fs *Filesystem) initSharedDaemon() (err error) {
	mp, err := fs.decideDaemonMountpoint(nil)
	if err != nil {
		return err
	}

	d, err := fs.createDaemon(mp, 1)
	if err != nil {
		return errors.Wrap(err, "initialize shared daemon")
	}

	// FIXME: Daemon record should not be removed after starting daemon failure.
	defer func() {
		if err != nil {
			if err := fs.Manager.DeleteDaemon(d); err != nil {
				log.L.Errorf("Start nydusd daemon error %v", err)
			}
		}
	}()

	// Shared nydusd daemon does not need configuration to start process but
	// it is loaded when requesting mount api
	// Dump the configuration file since it is reloaded when recovering the nydusd
	d.Config = fs.Manager.DaemonConfig
	err = d.Config.DumpFile(d.ConfigFile(""))
	if err != nil && !errors.Is(err, errdefs.ErrAlreadyExists) {
		return errors.Wrapf(err, "dump configuration file %s", d.ConfigFile(""))
	}

	if err := fs.Manager.StartDaemon(d); err != nil {
		return errors.Wrap(err, "start shared daemon")
	}

	fs.sharedDaemon = d

	return
}

func (fs *Filesystem) TryStopSharedDaemon() {
	sharedDaemon := fs.getSharedDaemon()
	if sharedDaemon != nil {
		if sharedDaemon.GetRef() == 1 {
			if err := fs.Manager.DestroyDaemon(sharedDaemon); err != nil {
				log.L.WithError(err).Errorf("Terminate shared daemon %s failed", sharedDaemon.ID())
			}
		}
	}
}

// createDaemon create new nydus daemon by snapshotID and imageID
// For fscache driver, no need to provide mountpoint to nydusd daemon.
func (fs *Filesystem) createDaemon(mountpoint string, ref int32) (d *daemon.Daemon, err error) {
	opts := []daemon.NewDaemonOpt{
		daemon.WithRef(ref),
		daemon.WithSocketDir(config.NydusConfig.SocketRoot()),
		daemon.WithConfigDir(config.NydusConfig.ConfigRoot()),
		daemon.WithLogDir(config.NydusConfig.LogDir),
		daemon.WithLogLevel(config.NydusConfig.LogLevel),
		daemon.WithLogToStdout(config.NydusConfig.LogToStdout),
		daemon.WithNydusdThreadNum(config.NydusConfig.NydusdThreadNum),
		daemon.WithFsDriver(config.GetFsDriver())}

	if mountpoint != "" {
		opts = append(opts, daemon.WithMountpoint(mountpoint))
	}

	d, err = daemon.NewDaemon(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "new daemon")
	}

	if err = fs.Manager.NewDaemon(d); err != nil {
		return nil, err
	}

	if fs.Manager.SupervisorSet != nil {
		// Supervisor is strongly associated with real running nydusd daemon.
		su := fs.Manager.SupervisorSet.NewSupervisor(d.ID())
		if su == nil {
			return nil, errors.Errorf("create supervisor for daemon %s", d.ID())

		}
		d.Supervisor = su
	}

	return d, nil
}

func (fs *Filesystem) DaemonBacked() bool {
	return config.NydusConfig.DaemonMode != command.DaemonModeNone
}
