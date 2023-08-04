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

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"

	snpkg "github.com/containerd/containerd/pkg/snapshotters"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/referrer"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
)

// TODO: refact `enabledManagers` and `xxxManager` into `ManagerCoordinator`
type Filesystem struct {
	fusedevSharedDaemon  *daemon.Daemon
	fscacheSharedDaemon  *daemon.Daemon
	blockdevManager      *manager.Manager
	fusedevManager       *manager.Manager
	fscacheManager       *manager.Manager
	nodevManager         *manager.Manager
	enabledManagers      []*manager.Manager
	cacheMgr             *cache.Manager
	referrerMgr          *referrer.Manager
	stargzResolver       *stargz.Resolver
	verifier             *signature.Verifier
	nydusImageBinaryPath string
	rootMountpoint       string
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

	recoveringDaemons := make(map[string]*daemon.Daemon, 0)
	liveDaemons := make(map[string]*daemon.Daemon, 0)
	for _, fsManager := range fs.enabledManagers {
		err := fsManager.Recover(ctx, &recoveringDaemons, &liveDaemons)
		if err != nil {
			return nil, errors.Wrap(err, "reconnect daemons and recover filesystem instance")
		}
	}

	var hasFscacheSharedDaemon = false
	var hasFusedevSharedDaemon = false
	for _, daemon := range liveDaemons {
		if daemon.States.FsDriver == config.FsDriverFscache {
			hasFscacheSharedDaemon = true
		} else if daemon.States.FsDriver == config.FsDriverFusedev && daemon.IsSharedDaemon() {
			hasFusedevSharedDaemon = true
		}
	}
	for _, daemon := range recoveringDaemons {
		if daemon.States.FsDriver == config.FsDriverFscache {
			hasFscacheSharedDaemon = true
		} else if daemon.States.FsDriver == config.FsDriverFusedev && daemon.IsSharedDaemon() {
			hasFusedevSharedDaemon = true
		}
	}

	// Try to bring up the shared daemon early.
	// With found recovering daemons, it must be the case that snapshotter is being restarted.
	// Situations that shared daemon is not found:
	//   1. The first time this nydus-snapshotter runs
	//   2. Daemon record is wrongly deleted from DB. Above reconnecting already gathers
	//		all daemons but still not found shared daemon. The best workaround is to start
	//		a new nydusd for it.
	// TODO: We still need to consider shared daemon the time sequence of initializing daemon,
	// start daemon commit its state to DB and retrieving its state.
	if fs.fscacheManager == nil {
		if hasFscacheSharedDaemon {
			return nil, errors.Errorf("shared fscache daemon is present, but manager is missing")
		}
	} else if !hasFscacheSharedDaemon && fs.fscacheSharedDaemon == nil {
		log.L.Infof("initializing shared nydus daemon for fscache")
		if err := fs.initSharedDaemon(fs.fscacheManager); err != nil {
			return nil, errors.Wrap(err, "start shared nydusd daemon for fscache")
		}
	}
	if fs.fusedevManager == nil {
		if hasFusedevSharedDaemon {
			return nil, errors.Errorf("shared fusedev daemon is present, but manager is missing")
		}
	} else if config.IsFusedevSharedModeEnabled() && !hasFusedevSharedDaemon && fs.fusedevSharedDaemon == nil {
		log.L.Infof("initializing shared nydus daemon for fusedev")
		if err := fs.initSharedDaemon(fs.fusedevManager); err != nil {
			return nil, errors.Wrap(err, "start shared nydusd daemon for fusedev")
		}
	}

	// Try to bring all persisted and stopped nydusd up and remount Rafs
	for _, d := range recoveringDaemons {
		d.ClearVestige()
		fsManager, err := fs.getManager(d.States.FsDriver)
		if err != nil {
			return nil, errors.Wrapf(err, "get filesystem manager for daemon %s", d.States.ID)
		}
		if err := fsManager.StartDaemon(d); err != nil {
			return nil, errors.Wrapf(err, "start daemon %s", d.ID())
		}
		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			return nil, errors.Wrapf(err, "wait for daemon %s", d.ID())
		}
		if err := d.RecoveredMountInstances(fsManager.AuthCache); err != nil {
			return nil, errors.Wrapf(err, "recover mounts for daemon %s", d.ID())
		}
		fs.TryRetainSharedDaemon(d)
	}

	for _, d := range liveDaemons {
		fs.TryRetainSharedDaemon(d)
	}

	return &fs, nil
}

func (fs *Filesystem) TryRetainSharedDaemon(d *daemon.Daemon) {
	if d.States.FsDriver == config.FsDriverFscache {
		if fs.fscacheSharedDaemon == nil {
			log.L.Debug("retain fscache shared daemon")
			fs.fscacheSharedDaemon = d
			d.IncRef()
		}
	} else if d.States.FsDriver == config.FsDriverFusedev {
		if fs.fusedevSharedDaemon == nil && d.HostMountpoint() == fs.rootMountpoint {
			log.L.Debug("retain fusedev shared daemon")
			fs.fusedevSharedDaemon = d
			d.IncRef()
		}
	}
}

func (fs *Filesystem) TryStopSharedDaemon() {
	if fs.fusedevSharedDaemon != nil {
		if fs.fusedevSharedDaemon.GetRef() == 1 {
			if err := fs.fusedevManager.DestroyDaemon(fs.fusedevSharedDaemon); err != nil {
				log.L.WithError(err).Errorf("Terminate shared daemon %s failed", fs.fusedevSharedDaemon.ID())
			}
		}
	}
	if fs.fscacheSharedDaemon != nil {
		if fs.fscacheSharedDaemon.GetRef() == 1 {
			if err := fs.fscacheManager.DestroyDaemon(fs.fscacheSharedDaemon); err != nil {
				log.L.WithError(err).Errorf("Terminate shared daemon %s failed", fs.fscacheSharedDaemon.ID())
			}
		}
	}
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

	if instance.GetFsDriver() == config.FsDriverFscache || instance.GetFsDriver() == config.FsDriverFusedev {
		d, err := fs.getDaemonByRafs(instance)
		if err != nil {
			return errors.Wrapf(err, "snapshot id %s daemon id %s", snapshotID, instance.DaemonID)
		}

		if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
			return err
		}

		log.L.Debugf("Nydus remote snapshot %s is ready", snapshotID)
	}

	return nil
}

// Mount will be called when containerd snapshotter prepare remote snapshotter
// this method will fork nydus daemon and manage it in the internal store, and indexed by snapshotID
// It must set up all necessary resources during Mount procedure and revoke any step if necessary.
func (fs *Filesystem) Mount(snapshotID string, labels map[string]string) (err error) {
	// TODO: support tarfs
	isTarfsMode := false
	fsDriver := config.GetFsDriver()
	if isTarfsMode {
		fsDriver = config.FsDriverBlockdev
	} else if !fs.DaemonBacked() {
		fsDriver = config.FsDriverNodev
	}
	isSharedFusedev := fsDriver == config.FsDriverFusedev && config.GetDaemonMode() == config.DaemonModeShared
	useSharedDaemon := fsDriver == config.FsDriverFscache || isSharedFusedev

	// Do not create RAFS instance in case of nodev.
	if fsDriver == config.FsDriverNodev {
		return nil
	}

	var imageID string
	imageID, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		// FIXME: Buildkit does not pass labels defined in containerd‘s fashion. So
		// we have to use stargz snapshotter specific labels until Buildkit generalize it the necessary
		// labels for all remote snapshotters.
		imageID, ok = labels["containerd.io/snapshot/remote/stargz.reference"]
		if !ok {
			return errors.Errorf("failed to find image ref of snapshot %s, labels %v",
				snapshotID, labels)
		}
	}

	r := daemon.RafsSet.Get(snapshotID)
	if r != nil {
		// Instance already exists, how could this happen? Can containerd handle this case?
		return nil
	}

	rafs, err := daemon.NewRafs(snapshotID, imageID, fsDriver)
	if err != nil {
		return errors.Wrapf(err, "create rafs instance %s", snapshotID)
	}

	defer func() {
		if err != nil {
			daemon.RafsSet.Remove(snapshotID)
		}
	}()

	fsManager, err := fs.getManager(fsDriver)
	if err != nil {
		return errors.Wrapf(err, "get filesystem manager for snapshot %s", snapshotID)
	}
	bootstrap, err := rafs.BootstrapFile()
	if err != nil {
		return errors.Wrapf(err, "find bootstrap file snapshot %s", snapshotID)
	}

	var d *daemon.Daemon
	if fsDriver == config.FsDriverFscache || fsDriver == config.FsDriverFusedev {
		if useSharedDaemon {
			d, err = fs.getSharedDaemon(fsDriver)
			if err != nil {
				return err
			}
		} else {
			mp, err := fs.decideDaemonMountpoint(fsDriver, false, rafs)
			if err != nil {
				return err
			}
			d, err = fs.createDaemon(fsManager, config.DaemonModeDedicated, mp, 0)
			// if daemon already exists for snapshotID, just return
			if err != nil && !errdefs.IsAlreadyExists(err) {
				return err
			}

			// TODO: reclaim resources on error
		}

		// Nydusd uses cache manager's directory to store blob caches. So cache
		// manager knows where to find those blobs.
		cacheDir := fs.cacheMgr.CacheDir()
		// Fscache driver stores blob cache bitmap and blob header files here
		workDir := rafs.FscacheWorkDir()
		params := map[string]string{
			daemonconfig.Bootstrap: bootstrap,
			daemonconfig.WorkDir:   workDir,
			daemonconfig.CacheDir:  cacheDir,
		}
		cfg := deepcopy.Copy(fsManager.DaemonConfig).(daemonconfig.DaemonConfig)
		err = daemonconfig.SupplementDaemonConfig(cfg, imageID, snapshotID, false, labels, params, func(imageHost string, keyChain *auth.PassKeyChain) error {
			return fsManager.AuthCache.UpdateAuth(imageHost, keyChain.ToBase64())
		})
		if err != nil {
			return errors.Wrap(err, "supplement configuration")
		}

		// TODO: How to manage rafs configurations on-disk? separated json config file or DB record?
		// In order to recover erofs mount, the configuration file has to be persisted.
		var configSubDir string
		if useSharedDaemon {
			configSubDir = snapshotID
		} else {
			// Associate daemon config object when creating a new daemon object to avoid
			// reading disk file again and again.
			// For shared daemon, each rafs instance has its own configuration, so we don't
			// attach a config interface to daemon in this case.
			d.Config = cfg
		}

		err = cfg.DumpFile(d.ConfigFile(configSubDir))
		if err != nil {
			if errors.Is(err, errdefs.ErrAlreadyExists) {
				log.L.Debugf("Configuration file %s already exits", d.ConfigFile(configSubDir))
			} else {
				return errors.Wrap(err, "dump daemon configuration file")
			}
		}

		d.AddInstance(rafs)
	}

	// if publicKey is not empty we should verify bootstrap file of image
	err = fs.verifier.Verify(labels, bootstrap)
	if err != nil {
		return errors.Wrapf(err, "verify signature of daemon %s", d.ID())
	}

	switch fsDriver {
	case config.FsDriverFscache:
		err = fs.mountRemote(fsManager, useSharedDaemon, d, rafs)
		if err != nil {
			return errors.Wrapf(err, "mount file system by daemon %s, snapshot %s", d.ID(), snapshotID)
		}
	case config.FsDriverFusedev:
		err = fs.mountRemote(fsManager, useSharedDaemon, d, rafs)
		if err != nil {
			return errors.Wrapf(err, "mount file system by daemon %s, snapshot %s", d.ID(), snapshotID)
		}
		// case config.FsDriverBlockdev:
		// TODO: support tarfs
	}

	// Persist it after associate instance after all the states are calculated.
	if err := fsManager.NewInstance(rafs); err != nil {
		return errors.Wrapf(err, "create instance %s", snapshotID)
	}

	return nil
}

func (fs *Filesystem) Umount(ctx context.Context, snapshotID string) error {
	instance := daemon.RafsSet.Get(snapshotID)
	if instance == nil {
		log.L.Debugf("no RAFS filesystem instance associated with snapshot %s", snapshotID)
		return nil
	}

	fsDriver := instance.GetFsDriver()
	fsManager, err := fs.getManager(fsDriver)
	if err != nil {
		return errors.Wrapf(err, "get manager for filesystem instance %s", instance.DaemonID)
	}

	if fsDriver == config.FsDriverFscache || fsDriver == config.FsDriverFusedev {
		daemon, err := fs.getDaemonByRafs(instance)
		if err != nil {
			log.L.Debugf("snapshot %s has no associated nydusd", snapshotID)
			return errors.Wrapf(err, "get daemon with ID %s for snapshot %s", instance.DaemonID, snapshotID)
		}

		daemon.RemoveInstance(snapshotID)
		if err := fsManager.RemoveInstance(snapshotID); err != nil {
			return errors.Wrapf(err, "remove snapshot %s", snapshotID)
		}
		if err := daemon.UmountInstance(instance); err != nil {
			return errors.Wrapf(err, "umount instance %s", snapshotID)
		}
		// Once daemon's reference reaches 0, destroy the whole daemon
		if daemon.GetRef() == 0 {
			if err := fsManager.DestroyDaemon(daemon); err != nil {
				return errors.Wrapf(err, "destroy daemon %s", daemon.ID())
			}
		}
		// } else if fsDriver == config.FsDriverBlockdev {
		// TODO: support tarfs
	}

	return nil
}

// How much space the layer/blob cache filesystem is occupying
// The blob digest mush have `sha256:` prefixed, otherwise, throw errors.
func (fs *Filesystem) CacheUsage(ctx context.Context, blobDigest string) (snapshots.Usage, error) {
	log.L.Infof("cache usage %s", blobDigest)
	digest := digest.Digest(blobDigest)
	if err := digest.Validate(); err != nil {
		return snapshots.Usage{}, errors.Wrapf(err, "invalid blob digest from label %q, digest=%s",
			snpkg.TargetLayerDigestLabel, blobDigest)
	}
	blobID := digest.Hex()
	return fs.cacheMgr.CacheUsage(ctx, blobID)
}

func (fs *Filesystem) RemoveCache(blobDigest string) error {
	log.L.Infof("remove cache %s", blobDigest)
	digest := digest.Digest(blobDigest)
	if err := digest.Validate(); err != nil {
		return errors.Wrapf(err, "invalid blob digest from label %q, digest=%s",
			snpkg.TargetLayerDigestLabel, blobDigest)
	}
	blobID := digest.Hex()

	if fs.fscacheManager != nil {
		c, err := fs.fscacheSharedDaemon.GetClient()
		if err != nil {
			return err
		}
		// delete fscache blob cache file
		// TODO: skip error for blob not existing
		if err := c.UnbindBlob("", blobID); err != nil {
			return err
		}
		return nil

	}

	return fs.cacheMgr.RemoveBlobCache(blobID)
}

// Try to stop all the running daemons if they are not referenced by any snapshots
// Clean up resources along with the daemons.
func (fs *Filesystem) Teardown(ctx context.Context) error {
	for _, fsManager := range fs.enabledManagers {
		if fsManager.FsDriver == config.FsDriverFscache || fsManager.FsDriver == config.FsDriverFusedev {
			for _, d := range fsManager.ListDaemons() {
				for _, instance := range d.Instances.List() {
					err := fs.Umount(ctx, instance.SnapshotID)
					if err != nil {
						log.L.Errorf("Failed to umount snapshot %s, %s", instance.SnapshotID, err)
					}
				}
			}
			// } else if fsManager.FsDriver == config.FsDriverBlockdev {
			// TODO: support tarfs
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
		return fs.rootMountpoint, nil
	}

	rafs := daemon.RafsSet.Get(snapshotID)
	if rafs != nil {
		return rafs.GetMountpoint(), nil
	}

	return "", errdefs.ErrNotFound
}

func (fs *Filesystem) BootstrapFile(id string) (string, error) {
	instance := daemon.RafsSet.Get(id)
	return instance.BootstrapFile()
}

// daemon mountpoint to rafs mountpoint
// calculate rafs mountpoint for snapshots mount slice.
func (fs *Filesystem) mountRemote(fsManager *manager.Manager, useSharedDaemon bool,
	d *daemon.Daemon, r *daemon.Rafs) error {

	if useSharedDaemon {
		if fsManager.FsDriver == config.FsDriverFusedev {
			r.SetMountpoint(path.Join(d.HostMountpoint(), r.SnapshotID))
		} else {
			r.SetMountpoint(path.Join(r.GetSnapshotDir(), "mnt"))
		}
		if err := d.SharedMount(r, fsManager.AuthCache); err != nil {
			return errors.Wrapf(err, "failed to mount")
		}
	} else {
		r.SetMountpoint(path.Join(d.HostMountpoint()))
		err := fsManager.StartDaemon(d)
		if err != nil {
			return errors.Wrapf(err, "start daemon")
		}
	}

	return nil
}

func (fs *Filesystem) decideDaemonMountpoint(fsDriver string, isSharedDaemonMode bool, rafs *daemon.Rafs) (string, error) {
	m := ""

	if fsDriver == config.FsDriverFscache || fsDriver == config.FsDriverFusedev {
		if isSharedDaemonMode {
			m = fs.rootMountpoint
		} else {
			m = path.Join(rafs.GetSnapshotDir(), "mnt")
		}
		if err := os.MkdirAll(m, 0755); err != nil {
			return "", errors.Wrapf(err, "create directory %s", m)
		}
	}

	return m, nil
}

// 1. Create a daemon instance
// 2. Build command line
// 3. Start daemon
func (fs *Filesystem) initSharedDaemon(fsManager *manager.Manager) (err error) {
	var daemonMode config.DaemonMode
	switch fsManager.FsDriver {
	case config.FsDriverFscache:
		daemonMode = config.DaemonModeShared
	case config.FsDriverFusedev:
		daemonMode = config.DaemonModeShared
	default:
		return errors.Errorf("unsupported filesystem driver %s", fsManager.FsDriver)
	}

	mp, err := fs.decideDaemonMountpoint(fsManager.FsDriver, true, nil)
	if err != nil {
		return err
	}

	d, err := fs.createDaemon(fsManager, daemonMode, mp, 0)
	if err != nil {
		return errors.Wrap(err, "initialize shared daemon")
	}

	// FIXME: Daemon record should not be removed after starting daemon failure.
	defer func() {
		if err != nil {
			if err := fsManager.DeleteDaemon(d); err != nil {
				log.L.Errorf("Start nydusd daemon error %v", err)
			}
		}
	}()

	// Shared nydusd daemon does not need configuration to start process but
	// it is loaded when requesting mount api
	// Dump the configuration file since it is reloaded when recovering the nydusd
	d.Config = fsManager.DaemonConfig
	err = d.Config.DumpFile(d.ConfigFile(""))
	if err != nil && !errors.Is(err, errdefs.ErrAlreadyExists) {
		return errors.Wrapf(err, "dump configuration file %s", d.ConfigFile(""))
	}

	if err := fsManager.StartDaemon(d); err != nil {
		return errors.Wrap(err, "start shared daemon")
	}

	fs.TryRetainSharedDaemon(d)

	return
}

// createDaemon create new nydus daemon by snapshotID and imageID
// For fscache driver, no need to provide mountpoint to nydusd daemon.
func (fs *Filesystem) createDaemon(fsManager *manager.Manager, daemonMode config.DaemonMode,
	mountpoint string, ref int32) (d *daemon.Daemon, err error) {
	opts := []daemon.NewDaemonOpt{
		daemon.WithRef(ref),
		daemon.WithSocketDir(config.GetSocketRoot()),
		daemon.WithConfigDir(config.GetConfigRoot()),
		daemon.WithLogDir(config.GetLogDir()),
		daemon.WithLogLevel(config.GetLogLevel()),
		daemon.WithLogRotationSize(config.GetDaemonLogRotationSize()),
		daemon.WithLogToStdout(config.GetLogToStdout()),
		daemon.WithNydusdThreadNum(config.GetDaemonThreadsNumber()),
		daemon.WithFsDriver(fsManager.FsDriver),
		daemon.WithDaemonMode(daemonMode),
	}

	if mountpoint != "" {
		opts = append(opts, daemon.WithMountpoint(mountpoint))
	}

	d, err = daemon.NewDaemon(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "new daemon")
	}

	if err = fsManager.NewDaemon(d); err != nil {
		return nil, err
	}

	if fsManager.SupervisorSet != nil {
		// Supervisor is strongly associated with real running nydusd daemon.
		su := fsManager.SupervisorSet.NewSupervisor(d.ID())
		if su == nil {
			return nil, errors.Errorf("create supervisor for daemon %s", d.ID())

		}
		d.Supervisor = su
	}

	return d, nil
}

func (fs *Filesystem) DaemonBacked() bool {
	return config.GetDaemonMode() != config.DaemonModeNone
}

func (fs *Filesystem) getManager(fsDriver string) (*manager.Manager, error) {
	switch fsDriver {
	case config.FsDriverBlockdev:
		if fs.blockdevManager != nil {
			return fs.blockdevManager, nil
		}
	case config.FsDriverFscache:
		if fs.fscacheManager != nil {
			return fs.fscacheManager, nil
		}
	case config.FsDriverFusedev:
		if fs.fusedevManager != nil {
			return fs.fusedevManager, nil
		}
	case config.FsDriverNodev:
		if fs.nodevManager != nil {
			return fs.nodevManager, nil
		}
	}

	return nil, errors.Errorf("no manager for filesystem driver %s", fsDriver)
}

func (fs *Filesystem) getSharedDaemon(fsDriver string) (*daemon.Daemon, error) {
	switch fsDriver {
	case config.FsDriverFscache:
		if fs.fscacheSharedDaemon != nil {
			return fs.fscacheSharedDaemon, nil
		}
	case config.FsDriverFusedev:
		if fs.fusedevSharedDaemon != nil {
			return fs.fusedevSharedDaemon, nil
		}
	}

	return nil, errors.Errorf("no shared daemon for filesystem driver %s", fsDriver)
}

func (fs *Filesystem) getDaemonByRafs(rafs *daemon.Rafs) (*daemon.Daemon, error) {
	switch rafs.GetFsDriver() {
	case config.FsDriverBlockdev:
		if fs.blockdevManager != nil {
			if d := fs.blockdevManager.GetByDaemonID(rafs.DaemonID); d != nil {
				return d, nil
			}
		}
	case config.FsDriverFscache:
		if fs.fscacheManager != nil {
			if d := fs.fscacheManager.GetByDaemonID(rafs.DaemonID); d != nil {
				return d, nil
			}
		}
	case config.FsDriverFusedev:
		if fs.fusedevManager != nil {
			if d := fs.fusedevManager.GetByDaemonID(rafs.DaemonID); d != nil {
				return d, nil
			}
		}
	}

	return nil, errdefs.ErrNotFound
}

func (fs *Filesystem) GetDaemonByID(id string) (*daemon.Daemon, error) {
	if fs.blockdevManager != nil {
		if d := fs.blockdevManager.GetByDaemonID(id); d != nil {
			return d, nil
		}
	}
	if fs.fscacheManager != nil {
		if d := fs.fscacheManager.GetByDaemonID(id); d != nil {
			return d, nil
		}
	}
	if fs.fusedevManager != nil {
		if d := fs.fusedevManager.GetByDaemonID(id); d != nil {
			return d, nil
		}
	}
	if fs.nodevManager != nil {
		if d := fs.nodevManager.GetByDaemonID(id); d != nil {
			return d, nil
		}
	}
	return nil, errdefs.ErrNotFound
}
