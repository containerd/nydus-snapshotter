/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	snpkg "github.com/containerd/containerd/v2/pkg/snapshotters"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"

	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/cgroup"
	v2 "github.com/containerd/nydus-snapshotter/pkg/cgroup/v2"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	mgr "github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics"
	"github.com/containerd/nydus-snapshotter/pkg/metrics/collector"
	"github.com/containerd/nydus-snapshotter/pkg/pprof"
	"github.com/containerd/nydus-snapshotter/pkg/referrer"
	"github.com/containerd/nydus-snapshotter/pkg/system"
	"github.com/containerd/nydus-snapshotter/pkg/tarfs"

	"github.com/containerd/nydus-snapshotter/pkg/store"

	"github.com/containerd/nydus-snapshotter/pkg/filesystem"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
)

var _ snapshots.Snapshotter = &snapshotter{}

type snapshotter struct {
	root                 string
	nydusdPath           string
	ms                   *storage.MetaStore // Storing snapshots' state, parentage and other metadata
	fs                   *filesystem.Filesystem
	cgroupManager        *cgroup.Manager
	enableNydusOverlayFS bool
	nydusOverlayFSPath   string
	enableKataVolume     bool
	syncRemove           bool
	cleanupOnClose       bool
}

func NewSnapshotter(ctx context.Context, cfg *config.SnapshotterConfig) (snapshots.Snapshotter, error) {
	verifier, err := signature.NewVerifier(cfg.ImageConfig.PublicKeyFile, cfg.ImageConfig.ValidateSignature)
	if err != nil {
		return nil, errors.Wrap(err, "initialize image verifier")
	}

	db, err := store.NewDatabase(cfg.Root)
	if err != nil {
		return nil, errors.Wrap(err, "create database")
	}

	rp, err := config.ParseRecoverPolicy(cfg.DaemonConfig.RecoverPolicy)
	if err != nil {
		return nil, errors.Wrap(err, "parse recover policy")
	}

	var cgroupMgr *cgroup.Manager
	if cfg.CgroupConfig.Enable {
		cgroupConfig, err := config.ParseCgroupConfig(cfg.CgroupConfig)
		if err != nil {
			return nil, errors.Wrap(err, "parse cgroup configuration")
		}
		log.L.Infof("parsed cgroup config: %#v", cgroupConfig)

		cgroupMgr, err = cgroup.NewManager(cgroup.Opt{
			Name:   "nydusd",
			Config: cgroupConfig,
		})
		if err != nil && (err != cgroup.ErrCgroupNotSupported || err != v2.ErrRootMemorySubtreeControllerDisabled) {
			return nil, errors.Wrap(err, "create cgroup manager")
		}
	}

	var skipSSLVerify bool
	var daemonConfig *daemonconfig.DaemonConfig
	fsDriver := config.GetFsDriver()
	if fsDriver == config.FsDriverFscache || fsDriver == config.FsDriverFusedev {
		config, err := daemonconfig.NewDaemonConfig(config.GetFsDriver(), cfg.DaemonConfig.NydusdConfigPath)
		if err != nil {
			return nil, errors.Wrap(err, "load daemon configuration")
		}
		daemonConfig = &config
		_, backendConfig := config.StorageBackend()
		skipSSLVerify = backendConfig.SkipVerify
	} else {
		skipSSLVerify = config.GetSkipSSLVerify()
	}

	fsManagers := []*mgr.Manager{}
	if cfg.Experimental.TarfsConfig.EnableTarfs {
		blockdevManager, err := mgr.NewManager(mgr.Opt{
			NydusdBinaryPath: "",
			Database:         db,
			CacheDir:         cfg.CacheManagerConfig.CacheDir,
			RootDir:          cfg.Root,
			RecoverPolicy:    rp,
			FsDriver:         config.FsDriverBlockdev,
			DaemonConfig:     nil,
			CgroupMgr:        cgroupMgr,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create blockdevice manager")
		}
		fsManagers = append(fsManagers, blockdevManager)
	}

	if config.GetFsDriver() == config.FsDriverFscache {
		fscacheManager, err := mgr.NewManager(mgr.Opt{
			NydusdBinaryPath: cfg.DaemonConfig.NydusdPath,
			Database:         db,
			CacheDir:         cfg.CacheManagerConfig.CacheDir,
			RootDir:          cfg.Root,
			RecoverPolicy:    rp,
			FsDriver:         config.FsDriverFscache,
			DaemonConfig:     daemonConfig,
			CgroupMgr:        cgroupMgr,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create fscache manager")
		}
		fsManagers = append(fsManagers, fscacheManager)
	}

	if config.GetFsDriver() == config.FsDriverFusedev {
		fusedevManager, err := mgr.NewManager(mgr.Opt{
			NydusdBinaryPath: cfg.DaemonConfig.NydusdPath,
			Database:         db,
			CacheDir:         cfg.CacheManagerConfig.CacheDir,
			RootDir:          cfg.Root,
			RecoverPolicy:    rp,
			FsDriver:         config.FsDriverFusedev,
			DaemonConfig:     daemonConfig,
			CgroupMgr:        cgroupMgr,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create fusedev manager")
		}
		fsManagers = append(fsManagers, fusedevManager)
	}

	if config.GetFsDriver() == config.FsDriverProxy {
		proxyManager, err := mgr.NewManager(mgr.Opt{
			NydusdBinaryPath: "",
			Database:         db,
			CacheDir:         cfg.CacheManagerConfig.CacheDir,
			RootDir:          cfg.Root,
			RecoverPolicy:    rp,
			FsDriver:         config.FsDriverProxy,
			DaemonConfig:     nil,
			CgroupMgr:        cgroupMgr,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create proxy manager")
		}
		fsManagers = append(fsManagers, proxyManager)
	}

	metricServer, err := metrics.NewServer(
		ctx,
		metrics.WithProcessManagers(fsManagers),
		metrics.WithHungIOInterval(cfg.MetricsConfig.HungIOInterval),
		metrics.WithCollectInterval(cfg.MetricsConfig.CollectInterval),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create metrics server")
	}

	// Start to collect metrics.
	if cfg.MetricsConfig.Address != "" {
		if err := metrics.NewMetricsHTTPListenerServer(cfg.MetricsConfig.Address); err != nil {
			return nil, errors.Wrap(err, "start metrics HTTP server")
		}
		go func() {
			if err := metricServer.StartCollectMetrics(ctx); err != nil {
				log.L.WithError(err).Errorf("Failed to start collecting metrics")
			}
		}()

		log.L.Infof("Started metrics HTTP server on %q", cfg.MetricsConfig.Address)
	}

	opts := []filesystem.NewFSOpt{
		filesystem.WithManagers(fsManagers),
		filesystem.WithNydusdBinaryPath(cfg.DaemonConfig.NydusdPath),
		filesystem.WithVerifier(verifier),
		filesystem.WithRootMountpoint(config.GetRootMountpoint()),
		filesystem.WithEnableStargz(cfg.Experimental.EnableStargz),
	}

	cacheConfig := &cfg.CacheManagerConfig
	cacheMgr, err := cache.NewManager(cache.Opt{
		Database: db,
		Period:   cfg.CacheManagerConfig.GCPeriod,
		CacheDir: cacheConfig.CacheDir,
		Disabled: cacheConfig.Disable,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create cache manager")
	}
	opts = append(opts, filesystem.WithCacheManager(cacheMgr))

	if cfg.Experimental.EnableReferrerDetect {
		referrerMgr := referrer.NewManager(skipSSLVerify)
		opts = append(opts, filesystem.WithReferrerManager(referrerMgr))
	}

	if cfg.Experimental.TarfsConfig.EnableTarfs {
		tarfsMgr := tarfs.NewManager(skipSSLVerify, cfg.Experimental.TarfsConfig.TarfsHint,
			cacheConfig.CacheDir, cfg.DaemonConfig.NydusImagePath,
			int64(cfg.Experimental.TarfsConfig.MaxConcurrentProc))
		opts = append(opts, filesystem.WithTarfsManager(tarfsMgr))
	}

	nydusFs, err := filesystem.NewFileSystem(ctx, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "initialize filesystem thin layer")
	}

	if config.IsSystemControllerEnabled() {
		systemController, err := system.NewSystemController(nydusFs, fsManagers, config.SystemControllerAddress())
		if err != nil {
			return nil, errors.Wrap(err, "create system controller")
		}

		go func() {
			if err := systemController.Run(); err != nil {
				log.L.WithError(err).Error("Failed to start system controller")
			}
		}()

		log.L.Infof("Started system controller on %q", config.SystemControllerAddress())

		pprofAddress := config.SystemControllerPprofAddress()
		if pprofAddress != "" {
			if err := pprof.NewPprofHTTPListener(pprofAddress); err != nil {
				return nil, errors.Wrap(err, "start pprof HTTP server")
			}

			log.L.Infof("Started pprof sever on %q", pprofAddress)
		}
	}

	supportsDType, err := getSupportsDType(cfg.Root)
	if err != nil {
		return nil, err
	}
	if !supportsDType {
		return nil, fmt.Errorf("%s does not support d_type. If the backing filesystem is xfs, please reformat with ftype=1 to enable d_type support", cfg.Root)
	}

	ms, err := storage.NewMetaStore(filepath.Join(cfg.Root, "metadata.db"))
	if err != nil {
		return nil, err
	}

	if err := os.Mkdir(filepath.Join(cfg.Root, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	syncRemove := cfg.SnapshotsConfig.SyncRemove
	if config.GetFsDriver() == config.FsDriverFscache {
		log.L.Infof("enable syncRemove for fscache mode")
		syncRemove = true
	}

	return &snapshotter{
		root:                 cfg.Root,
		nydusdPath:           cfg.DaemonConfig.NydusdPath,
		ms:                   ms,
		syncRemove:           syncRemove,
		fs:                   nydusFs,
		cgroupManager:        cgroupMgr,
		enableNydusOverlayFS: cfg.SnapshotsConfig.EnableNydusOverlayFS,
		nydusOverlayFSPath:   cfg.SnapshotsConfig.NydusOverlayFSPath,
		enableKataVolume:     cfg.SnapshotsConfig.EnableKataVolume,
		cleanupOnClose:       cfg.CleanupOnClose,
	}, nil
}

func (o *snapshotter) Cleanup(ctx context.Context) error {
	log.L.Debugf("[Cleanup] snapshots")
	if timer := collector.NewSnapshotMetricsTimer(collector.SnapshotMethodCleanup); timer != nil {
		defer timer.ObserveDuration()
	}

	cleanup, err := o.cleanupDirectories(ctx)
	if err != nil {
		return err
	}

	log.L.Infof("[Cleanup] orphan directories %v", cleanup)

	for _, dir := range cleanup {
		if err := o.cleanupSnapshotDirectory(ctx, dir); err != nil {
			log.L.WithError(err).Warnf("failed to remove directory %s", dir)
		}
	}
	return nil
}

func (o *snapshotter) Stat(ctx context.Context, key string) (snapshots.Info, error) {
	_, info, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
	return info, err
}

func (o *snapshotter) Update(ctx context.Context, info snapshots.Info, fieldpaths ...string) (snapshots.Info, error) {
	return snapshot.UpdateSnapshotInfo(ctx, o.ms, info, fieldpaths...)
}

func (o *snapshotter) Usage(ctx context.Context, key string) (snapshots.Usage, error) {
	id, info, usage, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
	if err != nil {
		return snapshots.Usage{}, err
	}

	switch info.Kind {
	case snapshots.KindActive:
		upperPath := o.upperPath(id)
		du, err := fs.DiskUsage(ctx, upperPath)
		if err != nil {
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	case snapshots.KindCommitted:
		// Caculate disk space usage under cacheDir of committed snapshots.
		if label.IsNydusDataLayer(info.Labels) || label.IsTarfsDataLayer(info.Labels) {
			if blobDigest, ok := info.Labels[snpkg.TargetLayerDigestLabel]; ok {
				// Try to get nydus meta layer/snapshot disk usage
				cacheUsage, err := o.fs.CacheUsage(ctx, blobDigest)
				if err != nil {
					return snapshots.Usage{}, errors.Wrapf(err, "try to get snapshot %s nydus disk usage", id)
				}
				usage.Add(cacheUsage)
			}
		}
	case snapshots.KindUnknown:
	case snapshots.KindView:
	}

	return usage, nil
}

func (o *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	log.L.Debugf("[Mounts] snapshot %s", key)
	if timer := collector.NewSnapshotMetricsTimer(collector.SnapshotMethodMount); timer != nil {
		defer timer.ObserveDuration()
	}
	var (
		needRemoteMounts = false
		metaSnapshotID   string
	)

	id, info, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
	if err != nil {
		return nil, errors.Wrapf(err, "mounts get snapshot %q info", key)
	}
	log.L.Infof("[Mounts] snapshot %s ID %s Kind %s", key, id, info.Kind)

	switch info.Kind {
	case snapshots.KindView:
		if label.IsNydusMetaLayer(info.Labels) {
			err = o.fs.WaitUntilReady(id)
			if err != nil {
				// Skip waiting if clients is unpacking nydus artifacts to `mounts`
				// For example, nydus-snapshotter's client like Buildkit is calling snapshotter in below workflow:
				//  1. [Prepare] snapshot for the uppermost layer - bootstrap
				//  2. [Mounts]
				//  3. Unpacking by applying the mounts, then we get bootstrap in its path position.
				// In above steps, no container write layer is called to set up from nydus-snapshotter. So it has no
				// chance to start nydusd, during which the Rafs instance is created.
				if !errors.Is(err, errdefs.ErrNotFound) {
					return nil, errors.Wrapf(err, "mounts: snapshot %s is not ready, err: %v", id, err)
				}
			} else {
				needRemoteMounts = true
				metaSnapshotID = id
			}
		} else if (o.fs.TarfsEnabled() && label.IsTarfsDataLayer(info.Labels)) || label.IsNydusProxyMode(info.Labels) {
			needRemoteMounts = true
			metaSnapshotID = id
		}
	case snapshots.KindActive:
		if info.Parent != "" {
			pKey := info.Parent
			if pID, pInfo, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, pKey); err == nil {
				if label.IsNydusMetaLayer(pInfo.Labels) {
					if err = o.fs.WaitUntilReady(pID); err != nil {
						return nil, errors.Wrapf(err, "mounts: snapshot %s is not ready, err: %v", pID, err)
					}
					needRemoteMounts = true
					metaSnapshotID = pID
				} else if (o.fs.TarfsEnabled() && label.IsTarfsDataLayer(pInfo.Labels)) || label.IsNydusProxyMode(pInfo.Labels) {
					needRemoteMounts = true
					metaSnapshotID = pID
				}
			} else {
				return nil, errors.Wrapf(err, "get parent snapshot info, parent key=%q", pKey)
			}
		}
	case snapshots.KindCommitted:
	case snapshots.KindUnknown:
	}

	if o.fs.ReferrerDetectEnabled() && !needRemoteMounts {
		if id, _, err := o.findReferrerLayer(ctx, key); err == nil {
			needRemoteMounts = true
			metaSnapshotID = id
		}
	}

	snap, err := snapshot.GetSnapshot(ctx, o.ms, key)
	if err != nil {
		return nil, errors.Wrapf(err, "get snapshot %s", key)
	}

	if treatAsProxyDriver(info.Labels) {
		log.L.Warnf("[Mounts] treat as proxy mode for the prepared snapshot by other snapshotter possibly: id = %s, labels = %v", id, info.Labels)
		return o.mountProxy(ctx, *snap)
	}

	if needRemoteMounts {
		return o.mountRemote(ctx, info.Labels, *snap, metaSnapshotID, key)
	}

	return o.mountNative(ctx, info.Labels, *snap)
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.L.Infof("[Prepare] snapshot with key %s parent %s", key, parent)

	if timer := collector.NewSnapshotMetricsTimer(collector.SnapshotMethodPrepare); timer != nil {
		defer timer.ObserveDuration()
	}

	logger := log.L.WithField("key", key).WithField("parent", parent)

	info, s, err := o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
	if err != nil {
		return nil, err
	}

	logger.Debugf("[Prepare] snapshot with labels %v", info.Labels)

	processor, target, err := chooseProcessor(ctx, logger, o, s, key, parent, info.Labels, func() string { return o.upperPath(s.ID) })
	if err != nil {
		return nil, err
	}

	needCommit, mounts, err := processor()

	if needCommit {
		err := o.Commit(ctx, target, key, append(opts, snapshots.WithLabels(info.Labels))...)
		if err == nil || errdefs.IsAlreadyExists(err) {
			return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "target snapshot %q", target)
		}
	}

	return mounts, err
}

// The work on supporting View operation for nydus-snapshotter is divided into 2 parts:
// 1. View on the topmost layer of nydus images or zran images
// 2. View on the any layer of nydus images or zran images
func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.L.Infof("[View] snapshot with key %s parent %s", key, parent)

	pID, pInfo, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, parent)
	if err != nil {
		return nil, errors.Wrapf(err, "get snapshot %s info", parent)
	}

	var (
		needRemoteMounts = false
		metaSnapshotID   string
	)

	if label.IsNydusMetaLayer(pInfo.Labels) {
		// Nydusd might not be running. We should run nydusd to reflect the rootfs.
		if err = o.fs.WaitUntilReady(pID); err != nil {
			if errors.Is(err, errdefs.ErrNotFound) {
				if err := o.fs.Mount(ctx, pID, pInfo.Labels, nil); err != nil {
					return nil, errors.Wrapf(err, "mount rafs, instance id %s", pID)
				}

				if err := o.fs.WaitUntilReady(pID); err != nil {
					return nil, errors.Wrapf(err, "wait for instance id %s", pID)
				}
			} else {
				return nil, errors.Wrapf(err, "daemon is not running %s", pID)
			}
		}

		needRemoteMounts = true
		metaSnapshotID = pID
	} else if label.IsNydusDataLayer(pInfo.Labels) {
		return nil, errors.New("only can view nydus topmost layer")
	}
	// Otherwise, it is OCI snapshots

	base, s, err := o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
	if err != nil {
		return nil, err
	}

	if o.fs.TarfsEnabled() && label.IsTarfsDataLayer(pInfo.Labels) {
		log.L.Infof("Prepare view snapshot %s in Nydus tarfs mode", pID)
		err = o.mergeTarfs(ctx, s, pID, pInfo)
		if err != nil {
			return nil, errors.Wrapf(err, "merge tarfs layers for snapshot %s", pID)
		}
		if err := o.fs.Mount(ctx, pID, pInfo.Labels, &s); err != nil {
			return nil, errors.Wrapf(err, "mount tarfs, snapshot id %s", pID)
		}
		log.L.Infof("Prepared view snapshot %s in Nydus tarfs mode", pID)
		needRemoteMounts = true
		metaSnapshotID = pID
	}

	if needRemoteMounts {
		return o.mountRemote(ctx, base.Labels, s, metaSnapshotID, key)
	}
	return o.mountNative(ctx, base.Labels, s)
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	log.L.Debugf("[Commit] snapshot with key %s", key)

	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if err := t.Rollback(); err != nil {
				log.L.WithError(err).Warn("failed to rollback transaction")
			}
		}
	}()

	// grab the existing id
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return err
	}

	log.L.Infof("[Commit] snapshot with key %q snapshot id %s", key, id)

	// For OCI compatibility, we calculate disk usage of the snapshotDir and commit the usage to DB.
	// Nydus disk usage under the cacheDir will be delayed until containerd queries.
	usage, err := fs.DiskUsage(ctx, o.upperPath(id))
	if err != nil {
		return err
	}

	if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return errors.Wrapf(err, "commit active snapshot %s", key)
	}

	// Let rollback catch the commit error
	err = t.Commit()
	if err != nil {
		return errors.Wrapf(err, "commit snapshot %s", key)
	}

	return err
}

func (o *snapshotter) Remove(ctx context.Context, key string) error {
	log.L.Debugf("[Remove] snapshot with key %s", key)
	if timer := collector.NewSnapshotMetricsTimer(collector.SnapshotMethodRemove); timer != nil {
		defer timer.ObserveDuration()
	}
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if err := t.Rollback(); err != nil {
				log.G(ctx).WithError(err).Warn("failed to rollback transaction")
			}
		}
	}()

	id, info, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return errors.Wrapf(err, "get snapshot %s", key)
	}

	switch {
	case label.IsNydusMetaLayer(info.Labels):
		log.L.Infof("[Remove] nydus meta snapshot with key %s snapshot id %s", key, id)
	case label.IsNydusDataLayer(info.Labels):
		log.L.Infof("[Remove] nydus data snapshot with key %s snapshot id %s", key, id)
	case label.IsTarfsDataLayer(info.Labels):
		log.L.Infof("[Remove] nydus tarfs snapshot with key %s snapshot id %s", key, id)
	default:
		// For example: remove snapshot with key sha256:c33c40022c8f333e7f199cd094bd56758bc479ceabf1e490bb75497bf47c2ebf
		log.L.Infof("[Remove] snapshot with key %s snapshot id %s", key, id)
	}

	if info.Kind == snapshots.KindCommitted {
		blobDigest := info.Labels[snpkg.TargetLayerDigestLabel]
		go func() {
			if err := o.fs.RemoveCache(blobDigest); err != nil {
				log.L.WithError(err).Errorf("Failed to remove cache %s", blobDigest)
			}
		}()
	}

	_, _, err = storage.Remove(ctx, key)
	if err != nil {
		return errors.Wrapf(err, "failed to remove key %s", key)
	}

	if o.syncRemove {
		var removals []string
		removals, err = o.getCleanupDirectories(ctx)
		if err != nil {
			return errors.Wrap(err, "get directories for removal")
		}

		// Remove directories after the transaction is closed, failures must not
		// return error since the transaction is committed with the removal
		// key no longer available.
		defer func() {
			if err == nil {
				for _, dir := range removals {
					if err := o.cleanupSnapshotDirectory(ctx, dir); err != nil {
						log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
					}
				}
			}
		}()
	}

	return t.Commit()
}

func (o *snapshotter) Walk(ctx context.Context, fn snapshots.WalkFunc, fs ...string) error {
	ctx, t, err := o.ms.TransactionContext(ctx, false)
	if err != nil {
		return err
	}
	defer func() {
		if err := t.Rollback(); err != nil {
			log.L.WithError(err)
		}
	}()

	return storage.WalkInfo(ctx, fn, fs...)
}

func (o *snapshotter) Close() error {
	log.L.Info("[Close] shutdown snapshotter")

	if o.cleanupOnClose {
		err := o.fs.Teardown(context.Background())
		if err != nil {
			log.L.Errorf("failed to clean up remote snapshot, err %v", err)
		}
	}

	o.fs.TryStopSharedDaemon()

	if o.cgroupManager != nil {
		if err := o.cgroupManager.Delete(); err != nil {
			log.L.Errorf("failed to destroy cgroup, err %v", err)
		}
	}

	return o.ms.Close()
}

func (o *snapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

// Get the rootdir of nydus image file system contents.
func (o *snapshotter) lowerPath(id string) (mnt string, err error) {
	if mnt, err = o.fs.MountPoint(id); err == nil {
		return mnt, nil
	} else if errors.Is(err, errdefs.ErrNotFound) {
		return filepath.Join(o.root, "snapshots", id, "fs"), nil
	}

	return "", err
}

func (o *snapshotter) workPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "work")
}

func (o *snapshotter) findReferrerLayer(ctx context.Context, key string) (string, snapshots.Info, error) {
	return snapshot.IterateParentSnapshots(ctx, o.ms, key, func(_ string, info snapshots.Info) bool {
		return o.fs.CheckReferrer(ctx, info.Labels)
	})
}

func (o *snapshotter) findMetaLayer(ctx context.Context, key string) (string, snapshots.Info, error) {
	return snapshot.IterateParentSnapshots(ctx, o.ms, key, func(_ string, i snapshots.Info) bool {
		return label.IsNydusMetaLayer(i.Labels)
	})
}

func (o *snapshotter) createSnapshot(ctx context.Context, kind snapshots.Kind, key, parent string, opts []snapshots.Opt) (info *snapshots.Info, _ storage.Snapshot, err error) {
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return nil, storage.Snapshot{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("failed to rollback transaction")
			}
		}
	}()

	var base snapshots.Info
	for _, opt := range opts {
		if err := opt(&base); err != nil {
			return &base, storage.Snapshot{}, err
		}
	}
	if base.Labels == nil {
		base.Labels = map[string]string{}
	}

	var td, path string
	defer func() {
		if td != "" {
			if err1 := o.cleanupSnapshotDirectory(ctx, td); err1 != nil {
				log.G(ctx).WithError(err1).Warn("failed to clean up temp snapshot directory")
			}
		}
		if path != "" {
			if err1 := o.cleanupSnapshotDirectory(ctx, path); err1 != nil {
				log.G(ctx).WithError(err1).WithField("path", path).Error("failed to reclaim snapshot directory, directory may need removal")
				err = errors.Wrapf(err, "failed to remove path: %v", err1)
			}
		}
	}()

	td, err = o.prepareDirectory(o.snapshotRoot(), kind)
	if err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "create prepare snapshot dir")
	}

	s, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "create snapshot")
	}

	// Try to keep the whole stack having the same UID and GID
	if len(s.ParentIDs) > 0 {
		st, err := os.Stat(o.upperPath(s.ParentIDs[0]))
		if err != nil {
			return nil, storage.Snapshot{}, errors.Wrap(err, "stat parent")
		}

		if err := lchown(filepath.Join(td, "fs"), st); err != nil {
			return nil, storage.Snapshot{}, errors.Wrap(err, "perform chown")
		}
	}

	path = o.snapshotDir(s.ID)
	if err = os.Rename(td, path); err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "perform rename")
	}
	td = ""

	rollback = false
	if err = t.Commit(); err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "perform commit")
	}
	path = ""

	return &base, s, nil
}

func (o *snapshotter) mergeTarfs(ctx context.Context, s storage.Snapshot, pID string, pInfo snapshots.Info) error {
	if err := o.fs.MergeTarfsLayers(s, func(id string) string { return o.upperPath(id) }); err != nil {
		return errors.Wrapf(err, "tarfs merge fail %s", pID)
	}
	if config.GetTarfsExportEnabled() {
		updateFields, err := o.fs.ExportBlockData(s, false, pInfo.Labels, func(id string) string { return o.upperPath(id) })
		if err != nil {
			return errors.Wrap(err, "export tarfs as block image")
		}
		if len(updateFields) > 0 {
			_, err = o.Update(ctx, pInfo, updateFields...)
			if err != nil {
				return errors.Wrapf(err, "update snapshot label information")
			}
		}
	}

	return nil
}

func bindMount(source, roFlag string) []mount.Mount {
	return []mount.Mount{
		{
			Type:   "bind",
			Source: source,
			Options: []string{
				roFlag,
				"rbind",
			},
		},
	}
}

func overlayMount(options []string) []mount.Mount {
	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}
}

// Handle proxy mount which the snapshot has been prepared by other snapshotter, mainly used for pause image in containerd
func (o *snapshotter) mountProxy(ctx context.Context, s storage.Snapshot) ([]mount.Mount, error) {
	var overlayOptions []string
	if s.Kind == snapshots.KindActive {
		overlayOptions = append(overlayOptions,
			fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
		)
	}

	log.G(ctx).Debugf("len(s.ParentIDs) = %v", len(s.ParentIDs))
	parentPaths := make([]string, 0, len(s.ParentIDs)+1)
	if len(s.ParentIDs) == 0 {
		parentPaths = append(parentPaths, config.GetSnapshotsRootDir())
	} else {
		for _, id := range s.ParentIDs {
			parentPaths = append(parentPaths, o.upperPath(id))
		}
	}

	lowerDirOption := fmt.Sprintf("lowerdir=%s", strings.Join(parentPaths, ":"))
	overlayOptions = append(overlayOptions, lowerDirOption)
	log.G(ctx).Infof("proxy mount options %v", overlayOptions)
	options, err := o.mountWithProxyVolume(rafs.Rafs{
		FsDriver:    config.GetFsDriver(),
		Annotations: make(map[string]string),
	})
	if err != nil {
		return []mount.Mount{}, errors.Wrapf(err, "create kata volume for proxy")
	}
	if len(options) > 0 {
		overlayOptions = append(overlayOptions, options...)
	}
	log.G(ctx).Debugf("fuse.nydus-overlayfs mount options %v", overlayOptions)

	mountType := "fuse.nydus-overlayfs"
	if o.nydusOverlayFSPath != "" {
		log.G(ctx).Debugf("Using nydus-overlayfs from path: %s", o.nydusOverlayFSPath)
		mountType = fmt.Sprintf("fuse.%s", o.nydusOverlayFSPath)
	}

	mounts := []mount.Mount{
		{
			Type:    mountType,
			Source:  "overlay",
			Options: overlayOptions,
		},
	}
	return mounts, nil
}

// `s` is the upmost snapshot and `id` refers to the nydus meta snapshot
// `s` and `id` can represent a different layer, it's useful when View an image
func (o *snapshotter) mountRemote(ctx context.Context, labels map[string]string, s storage.Snapshot, id, key string) ([]mount.Mount, error) {
	var overlayOptions []string
	if _, ok := labels[label.OverlayfsVolatileOpt]; ok {
		overlayOptions = append(overlayOptions, "volatile")
	}

	lowerPaths := make([]string, 0, 8)
	if o.fs.ReferrerDetectEnabled() {
		// From the parent list, we want to add all the layers
		// between the upmost snapshot and the nydus meta snapshot.
		// On the other hand, we consider that all the layers below
		// the nydus meta snapshot will be included in its mount.
		for i := range s.ParentIDs {
			if s.ParentIDs[i] == id {
				break
			}
			lowerPaths = append(lowerPaths, o.upperPath(s.ParentIDs[i]))
		}
	}

	lowerPathNydus, err := o.lowerPath(id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to locate overlay lowerdir")
	}
	lowerPaths = append(lowerPaths, lowerPathNydus)

	if s.Kind == snapshots.KindActive {
		overlayOptions = append(overlayOptions,
			fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
		)
	} else if s.Kind == snapshots.KindView {
		lowerPathNormal, err := o.lowerPath(s.ID)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to locate overlay lowerdir for view snapshot")
		}
		lowerPaths = append(lowerPaths, lowerPathNormal)
	}

	lowerDirOption := fmt.Sprintf("lowerdir=%s", strings.Join(lowerPaths, ":"))
	overlayOptions = append(overlayOptions, lowerDirOption)
	log.G(ctx).Infof("remote mount options %v", overlayOptions)

	if o.enableKataVolume {
		return o.mountWithKataVolume(ctx, id, overlayOptions, key)
	}
	// Add `extraoption` if NydusOverlayFS is enable or daemonMode is `None`
	if o.enableNydusOverlayFS || config.GetDaemonMode() == config.DaemonModeNone {
		return o.remoteMountWithExtraOptions(ctx, s, id, overlayOptions)
	}
	return overlayMount(overlayOptions), nil
}

func (o *snapshotter) mountNative(ctx context.Context, labels map[string]string, s storage.Snapshot) ([]mount.Mount, error) {
	if len(s.ParentIDs) == 0 {
		// if we only have one layer/no parents then just return a bind mount as overlay will not work
		roFlag := "rw"
		if s.Kind == snapshots.KindView {
			roFlag = "ro"
		}
		return bindMount(o.upperPath(s.ID), roFlag), nil
	}

	var options []string
	if s.Kind == snapshots.KindActive {
		options = append(options,
			fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
		)
		if _, ok := labels[label.OverlayfsVolatileOpt]; ok {
			options = append(options, "volatile")
		}
	} else if len(s.ParentIDs) == 1 {
		return bindMount(o.upperPath(s.ID), "ro"), nil
	}

	parentPaths := make([]string, len(s.ParentIDs))
	for i := range s.ParentIDs {
		parentPaths[i] = o.upperPath(s.ParentIDs[i])
	}
	options = append(options, fmt.Sprintf("lowerdir=%s", strings.Join(parentPaths, ":")))

	log.G(ctx).Debugf("overlayfs mount options %s", options)
	return overlayMount(options), nil
}

func (o *snapshotter) prepareDirectory(snapshotDir string, kind snapshots.Kind) (string, error) {
	td, err := os.MkdirTemp(snapshotDir, "new-")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp dir")
	}

	if err := os.Mkdir(filepath.Join(td, "fs"), 0755); err != nil {
		return td, err
	}

	if kind == snapshots.KindActive {
		if err := os.Mkdir(filepath.Join(td, "work"), 0711); err != nil {
			return td, err
		}
	}

	return td, nil
}

func (o *snapshotter) getCleanupDirectories(ctx context.Context) ([]string, error) {
	ids, err := storage.IDMap(ctx)
	if err != nil {
		return nil, err
	}

	// For example:
	// 23:default/24/sha256:8c2ed532dce363da2d08489f385c432f7c0ee4509ab4e20eb2778803916adc93
	// 24:sha256:c858413d9e5162c287028d630128ea4323d5029bf8a093af783480b38cf8d44e
	// 25:sha256:fcb51e3c865d96542718beba0bb247376e4c78e039412c5d30c989872e66b6d5

	fd, err := os.Open(o.snapshotRoot())
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	dirs, err := fd.Readdirnames(0)
	if err != nil {
		return nil, err
	}

	cleanup := make([]string, 0, 16)
	for _, d := range dirs {
		if _, ok := ids[d]; ok {
			continue
		}
		// When it quits, there will be nothing inside
		// TODO: try to clean up config/sockets/logs directories
		cleanup = append(cleanup, o.snapshotDir(d))
	}
	return cleanup, nil
}

func (o *snapshotter) cleanupDirectories(ctx context.Context) ([]string, error) {
	// Get a write transaction to ensure no other write transaction can be entered
	// while the cleanup is scanning.
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := t.Rollback(); err != nil {
			log.L.WithError(err)
		}
	}()

	return o.getCleanupDirectories(ctx)
}

func (o *snapshotter) cleanupSnapshotDirectory(ctx context.Context, dir string) error {
	// For example: cleanupSnapshotDirectory /var/lib/containerd/io.containerd.snapshotter.v1.nydus/snapshots/34" dir=/var/lib/containerd/io.containerd.snapshotter.v1.nydus/snapshots/34

	snapshotID := filepath.Base(dir)
	if err := o.fs.Umount(ctx, snapshotID); err != nil && !os.IsNotExist(err) {
		log.G(ctx).WithError(err).WithField("dir", dir).Error("failed to unmount")
	}

	if o.fs.TarfsEnabled() {
		if err := o.fs.DetachTarfsLayer(snapshotID); err != nil && !os.IsNotExist(err) {
			log.G(ctx).WithError(err).Errorf("failed to detach tarfs layer for snapshot %s", snapshotID)
		}
	}

	if err := os.RemoveAll(dir); err != nil {
		return errors.Wrapf(err, "remove directory %q", dir)
	}

	return nil
}

func (o *snapshotter) snapshotRoot() string {
	return filepath.Join(o.root, "snapshots")
}

func (o *snapshotter) snapshotDir(id string) string {
	return filepath.Join(o.snapshotRoot(), id)
}

func treatAsProxyDriver(labels map[string]string) bool {
	isProxyDriver := config.GetFsDriver() == config.FsDriverProxy
	isProxyLabel := label.IsNydusProxyMode(labels)
	_, isProxyImage := labels[label.CRIImageRef]
	log.G(context.Background()).Debugf("isProxyDriver = %t, isProxyLabel = %t, isProxyImage = %t", isProxyDriver, isProxyLabel, isProxyImage)
	switch {
	case isProxyDriver && isProxyImage:
		return false
	case isProxyDriver != isProxyLabel:
		log.G(context.Background()).Warnf("check Labels With Driver failed, driver = %q, labels = %q", config.GetFsDriver(), labels)
		return true
	default:
		return false
	}
}
