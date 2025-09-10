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

	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/mohae/deepcopy"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/log"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	racache "github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/referrer"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/tarfs"
)

type Filesystem struct {
	fusedevSharedDaemon *daemon.Daemon
	fscacheSharedDaemon *daemon.Daemon
	enabledManagers     map[string]*manager.Manager
	cacheMgr            *cache.Manager
	referrerMgr         *referrer.Manager
	stargzResolver      *stargz.Resolver
	tarfsMgr            *tarfs.Manager
	verifier            *signature.Verifier
	nydusdBinaryPath    string
	rootMountpoint      string
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
	if fscacheManager, ok := fs.enabledManagers[config.FsDriverFscache]; ok {
		if !hasFscacheSharedDaemon && fs.fscacheSharedDaemon == nil {
			log.L.Infof("initializing shared nydus daemon for fscache")
			if err := fs.initSharedDaemon(fscacheManager); err != nil {
				return nil, errors.Wrap(err, "start shared nydusd daemon for fscache")
			}
		}
	} else if hasFscacheSharedDaemon {
		return nil, errors.Errorf("shared fscache daemon is present, but manager is missing")
	}
	if fusedevManager, ok := fs.enabledManagers[config.FsDriverFusedev]; ok {
		if config.IsFusedevSharedModeEnabled() && !hasFusedevSharedDaemon && fs.fusedevSharedDaemon == nil {
			log.L.Infof("initializing shared nydus daemon for fusedev")
			if err := fs.initSharedDaemon(fusedevManager); err != nil {
				return nil, errors.Wrap(err, "start shared nydusd daemon for fusedev")
			}
		}
	} else if hasFusedevSharedDaemon {
		return nil, errors.Errorf("shared fusedev daemon is present, but manager is missing")
	}

	// Try to bring all persisted and stopped nydusd up and remount Rafs
	egRecover, _ := errgroup.WithContext(context.Background())
	for _, d := range recoveringDaemons {
		d := d
		egRecover.Go(func() error {
			d.ClearVestige()
			fsManager, err := fs.getManager(d.States.FsDriver)
			if err != nil {
				log.L.Warnf("Failed to get filesystem manager for daemon %s, skipping recovery: %v", d.States.ID, err)
				return nil
			}
			if err := fsManager.StartDaemon(d); err != nil {
				log.L.Warnf("Failed to start daemon %s during recovery, skipping: %v", d.ID(), err)
				return nil
			}
			if err := d.WaitUntilState(types.DaemonStateRunning); err != nil {
				log.L.Warnf("Failed to wait for daemon %s to become running, skipping: %v", d.ID(), err)
				return nil
			}
			if err := d.RecoverRafsInstances(); err != nil {
				log.L.Warnf("Failed to recover mounts for daemon %s, skipping: %v", d.ID(), err)
				return nil
			}
			fs.TryRetainSharedDaemon(d)
			return nil
		})
	}
	if err := egRecover.Wait(); err != nil {
		return nil, err
	}

	newNydusImageBinaryCommit, err := daemon.GetDaemonGitCommit(fs.nydusdBinaryPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get git commit from nydusd binary at path: %s", fs.nydusdBinaryPath)
	}

	egLive, _ := errgroup.WithContext(context.Background())
	for _, d := range liveDaemons {
		d := d
		egLive.Go(func() error {
			if d.Supervisor == nil {
				log.L.Warnf("Daemon %s is skipped for hot upgrade because recover policy is not set to 'failover'", d.ID())
				return nil
			}
			daemonInfo, err := d.GetDaemonInfo()
			if err != nil {
				log.L.Warnf("Failed to get daemon info from daemon %s, skipping: %v", d.ID(), err)
				return nil
			}
			if newNydusImageBinaryCommit != daemonInfo.DaemonVersion().GitCommit {
				fsManager, err := fs.getManager(d.States.FsDriver)
				if err != nil {
					log.L.Warnf("Failed to get filesystem manager for daemon %s, skipping: %v", d.ID(), err)
					return nil
				}
				newDaemon, upgradeErr := fsManager.DoDaemonUpgrade(d, fs.nydusdBinaryPath, fsManager)
				if upgradeErr != nil {
					log.L.Warnf("Daemon %s hot upgrade failed, skipping: %v", d.ID(), upgradeErr)
					return nil
				}
				fs.TryRetainSharedDaemon(newDaemon)
			} else {
				fs.TryRetainSharedDaemon(d)
			}
			return nil
		})
	}
	if err := egLive.Wait(); err != nil {
		return nil, err
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
			if fusedevManager, ok := fs.enabledManagers[config.FsDriverFusedev]; ok {
				if err := fusedevManager.DestroyDaemon(fs.fusedevSharedDaemon); err != nil {
					log.L.WithError(err).Errorf("Terminate shared daemon %s failed", fs.fusedevSharedDaemon.ID())
				} else {
					fs.fusedevSharedDaemon = nil
				}
			}
		}
	}
	if fs.fscacheSharedDaemon != nil {
		if fs.fscacheSharedDaemon.GetRef() == 1 {
			if fscacheManager, ok := fs.enabledManagers[config.FsDriverFscache]; ok {
				if err := fscacheManager.DestroyDaemon(fs.fscacheSharedDaemon); err != nil {
					log.L.WithError(err).Errorf("Terminate shared daemon %s failed", fs.fscacheSharedDaemon.ID())
				} else {
					fs.fscacheSharedDaemon = nil
				}
			}
		}
	}
}

// WaitUntilReady wait until daemon ready by snapshotID, it will wait until nydus domain socket established
// and the status of nydusd daemon must be ready
func (fs *Filesystem) WaitUntilReady(snapshotID string) error {
	rafs := racache.RafsGlobalCache.Get(snapshotID)
	if rafs == nil {
		// If NoneDaemon mode, there's no need to wait for daemon ready
		if config.GetDaemonMode() == config.DaemonModeNone {
			return nil
		}
		return errors.Wrapf(errdefs.ErrNotFound, "no instance %s", snapshotID)
	}

	if rafs.GetFsDriver() == config.FsDriverFscache || rafs.GetFsDriver() == config.FsDriverFusedev {
		d, err := fs.getDaemonByRafs(rafs)
		if err != nil {
			return errors.Wrapf(err, "snapshot id %s daemon id %s", snapshotID, rafs.DaemonID)
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
func (fs *Filesystem) Mount(ctx context.Context, snapshotID string, labels map[string]string, s *storage.Snapshot) (err error) {
	rafs := racache.RafsGlobalCache.Get(snapshotID)
	if rafs != nil {
		// Instance already exists, how could this happen? Can containerd handle this case?
		return nil
	}

	fsDriver := config.GetFsDriver()
	if label.IsTarfsDataLayer(labels) {
		fsDriver = config.FsDriverBlockdev
	}
	isSharedFusedev := fsDriver == config.FsDriverFusedev && config.GetDaemonMode() == config.DaemonModeShared
	useSharedDaemon := fsDriver == config.FsDriverFscache || isSharedFusedev

	var imageID string
	imageID, ok := labels[snpkg.TargetRefLabel]
	if !ok {
		// FIXME: Buildkit does not pass labels defined in containerd's fashion. So
		// we have to use stargz snapshotter specific labels until Buildkit generalize it the necessary
		// labels for all remote snapshotters.
		imageID, ok = labels["containerd.io/snapshot/remote/stargz.reference"]
		if !ok {
			return errors.Errorf("failed to find image ref of snapshot %s, labels %v", snapshotID, labels)
		}
	}

	rafs, err = racache.NewRafs(snapshotID, imageID, fsDriver)
	if err != nil {
		return errors.Wrapf(err, "create rafs instance %s", snapshotID)
	}

	defer func() {
		if err != nil {
			racache.RafsGlobalCache.Remove(snapshotID)
		}
	}()

	fsManager, err := fs.getManager(fsDriver)
	if err != nil {
		return errors.Wrapf(err, "get filesystem manager for snapshot %s", snapshotID)
	}

	var d *daemon.Daemon
	if fsDriver == config.FsDriverFscache || fsDriver == config.FsDriverFusedev {
		bootstrap, err := rafs.BootstrapFile()
		if err != nil {
			return errors.Wrapf(err, "find bootstrap file snapshot %s", snapshotID)
		}

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
		cfg := deepcopy.Copy(*fsManager.DaemonConfig).(daemonconfig.DaemonConfig)
		err = daemonconfig.SupplementDaemonConfig(cfg, imageID, snapshotID, false, labels, params)
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

		d.AddRafsInstance(rafs)

		// if publicKey is not empty we should verify bootstrap file of image
		err = fs.verifier.Verify(labels, bootstrap)
		if err != nil {
			return errors.Wrapf(err, "verify signature of daemon %s", d.ID())
		}
	}

	switch fsDriver {
	case config.FsDriverFscache:
		err = fs.mountRemote(fsManager, useSharedDaemon, d, rafs)
		if err != nil {
			err = errors.Wrapf(err, "mount file system by daemon %s, snapshot %s", d.ID(), snapshotID)
		}
	case config.FsDriverFusedev:
		err = fs.mountRemote(fsManager, useSharedDaemon, d, rafs)
		if err != nil {
			err = errors.Wrapf(err, "mount file system by daemon %s, snapshot %s", d.ID(), snapshotID)
		}
	case config.FsDriverBlockdev:
		err = fs.tarfsMgr.MountTarErofs(snapshotID, s, labels, rafs)
		if err != nil {
			err = errors.Wrapf(err, "mount tarfs for snapshot %s", snapshotID)
		}
	case config.FsDriverNodev:
		// Nothing to do
	case config.FsDriverProxy:
		if label.IsNydusProxyMode(labels) {
			if v, ok := labels[label.CRILayerDigest]; ok {
				rafs.AddAnnotation(label.CRILayerDigest, v)
			}
			rafs.AddAnnotation(label.NydusProxyMode, "true")
			rafs.SetMountpoint(path.Join(rafs.GetSnapshotDir(), "fs"))
		}
	default:
		err = errors.Errorf("unknown filesystem driver %s for snapshot %s", fsDriver, snapshotID)
	}

	// Persist it after associate instance after all the states are calculated.
	if err == nil {
		if err := fsManager.AddRafsInstance(rafs); err != nil {
			// In the CoCo scenario, the existence of a rafs instance is not a concern, as the CoCo guest image pull
			// does not utilize snapshots on the host. Therefore, we expect it to pass normally regardless of its existence.
			// However, for the convenience of troubleshooting, we tend to print relevant logs.
			if config.GetFsDriver() == config.FsDriverProxy {
				log.L.Warnf("RAFS instance has associated with snapshot %s possibly: %v", snapshotID, err)
				return nil
			}
			return errors.Wrapf(err, "create instance %s", snapshotID)
		}
	}

	if err != nil {
		_ = fs.Umount(ctx, snapshotID)
		return err
	}

	return nil
}

func (fs *Filesystem) Umount(_ context.Context, snapshotID string) error {
	rafs := racache.RafsGlobalCache.Get(snapshotID)
	if rafs == nil {
		log.L.Debugf("no RAFS filesystem instance associated with snapshot %s", snapshotID)
		return nil
	}

	fsDriver := rafs.GetFsDriver()
	if fsDriver == config.FsDriverNodev {
		return nil
	}
	fsManager, err := fs.getManager(fsDriver)
	if err != nil {
		return errors.Wrapf(err, "get manager for filesystem instance %s", rafs.DaemonID)
	}

	switch fsDriver {
	case config.FsDriverFscache, config.FsDriverFusedev:
		daemon, err := fs.getDaemonByRafs(rafs)
		if err != nil {
			log.L.Debugf("snapshot %s has no associated nydusd", snapshotID)
			return errors.Wrapf(err, "get daemon with ID %s for snapshot %s", rafs.DaemonID, snapshotID)
		}

		daemon.RemoveRafsInstance(snapshotID)
		if err := fsManager.RemoveRafsInstance(snapshotID); err != nil {
			return errors.Wrapf(err, "remove snapshot %s", snapshotID)
		}
		if err := daemon.UmountRafsInstance(rafs); err != nil {
			return errors.Wrapf(err, "umount instance %s", snapshotID)
		}
		// Once daemon's reference reaches 0, destroy the whole daemon
		if daemon.GetRef() == 0 {
			if err := fsManager.DestroyDaemon(daemon); err != nil {
				return errors.Wrapf(err, "destroy daemon %s", daemon.ID())
			}
		}
	case config.FsDriverBlockdev:
		if err := fs.tarfsMgr.UmountTarErofs(snapshotID); err != nil {
			return errors.Wrapf(err, "umount tar erofs on snapshot %s", snapshotID)
		}
		if err := fsManager.RemoveRafsInstance(snapshotID); err != nil {
			return errors.Wrapf(err, "remove snapshot %s", snapshotID)
		}
	case config.FsDriverNodev, config.FsDriverProxy:
		// Nothing to do
	default:
		return errors.Errorf("unknown filesystem driver %s for snapshot %s", fsDriver, snapshotID)
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

	if fscacheManager, ok := fs.enabledManagers[config.FsDriverFscache]; ok {
		if fscacheManager != nil {
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
	}

	return fs.cacheMgr.RemoveBlobCache(blobID)
}

// Try to stop all the running daemons if they are not referenced by any snapshots
// Clean up resources along with the daemons.
func (fs *Filesystem) Teardown(ctx context.Context) error {
	for _, fsManager := range fs.enabledManagers {
		if fsManager.FsDriver == config.FsDriverFscache || fsManager.FsDriver == config.FsDriverFusedev {
			for _, d := range fsManager.ListDaemons() {
				for _, instance := range d.RafsCache.List() {
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
	rafs := racache.RafsGlobalCache.Get(snapshotID)
	if rafs != nil {
		return rafs.GetMountpoint(), nil
	}

	return "", errdefs.ErrNotFound
}

func (fs *Filesystem) BootstrapFile(id string) (string, error) {
	rafs := racache.RafsGlobalCache.Get(id)
	if rafs == nil {
		return "", errors.Errorf("no RAFS instance for %s", id)
	}
	return rafs.BootstrapFile()
}

// daemon mountpoint to rafs mountpoint
// calculate rafs mountpoint for snapshots mount slice.
func (fs *Filesystem) mountRemote(fsManager *manager.Manager, useSharedDaemon bool,
	d *daemon.Daemon, r *racache.Rafs) error {

	if useSharedDaemon {
		if fsManager.FsDriver == config.FsDriverFusedev {
			r.SetMountpoint(path.Join(d.HostMountpoint(), r.SnapshotID))
		} else {
			r.SetMountpoint(path.Join(r.GetSnapshotDir(), "mnt"))
		}
		if err := d.SharedMount(r); err != nil {
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

func (fs *Filesystem) decideDaemonMountpoint(fsDriver string, isSharedDaemonMode bool, rafs *racache.Rafs) (string, error) {
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
	} else if mp == "" {
		return errors.Errorf("got null mountpoint for fsDriver %s", fsManager.FsDriver)
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
	d.Config = *fsManager.DaemonConfig
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

	// For fscache driver, no need to provide mountpoint to nydusd daemon.
	if mountpoint != "" {
		opts = append(opts, daemon.WithMountpoint(mountpoint))
	}

	d, err = daemon.NewDaemon(opts...)
	if err != nil {
		return nil, errors.Wrapf(err, "new daemon")
	}

	if err = fsManager.AddDaemon(d); err != nil {
		return nil, err
	}

	if fsManager.SupervisorSet != nil {
		// Supervisor is strongly associated with real running nydusd daemon.
		su := fsManager.SupervisorSet.NewSupervisor(d.ID())
		if su == nil {
			_ = fsManager.DeleteDaemon(d)
			return nil, errors.Errorf("create supervisor for daemon %s", d.ID())
		}
		d.Supervisor = su
	}

	return d, nil
}

func (fs *Filesystem) getManager(fsDriver string) (*manager.Manager, error) {
	if fsManager, ok := fs.enabledManagers[fsDriver]; ok {
		return fsManager, nil
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

func (fs *Filesystem) getDaemonByRafs(rafs *racache.Rafs) (*daemon.Daemon, error) {
	switch rafs.GetFsDriver() {
	case config.FsDriverFscache, config.FsDriverFusedev:
		if fsManager, ok := fs.enabledManagers[rafs.GetFsDriver()]; ok {
			if d := fsManager.GetByDaemonID(rafs.DaemonID); d != nil {
				return d, nil
			}
		}
	}

	return nil, errdefs.ErrNotFound
}

func (fs *Filesystem) GetDaemonByID(id string) (*daemon.Daemon, error) {
	for _, manager := range fs.enabledManagers {
		if d := manager.GetByDaemonID(id); d != nil {
			return d, nil
		}
	}
	return nil, errdefs.ErrNotFound
}
