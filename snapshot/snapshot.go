/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"

	blob "github.com/containerd/nydus-snapshotter/pkg/blob"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/layout"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/metrics"

	"github.com/containerd/nydus-snapshotter/pkg/resolve"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/containerd/nydus-snapshotter/pkg/system"

	fspkg "github.com/containerd/nydus-snapshotter/pkg/filesystem/fs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
)

var _ snapshots.Snapshotter = &snapshotter{}

type snapshotter struct {
	root       string
	nydusdPath string
	// Storing snapshots' state, parentage ant other metadata
	ms                   *storage.MetaStore
	fs                   *fspkg.Filesystem
	blobMgr              *blob.Manager
	manager              *manager.Manager
	hasDaemon            bool
	enableNydusOverlayFS bool
	syncRemove           bool
	cleanupOnClose       bool
}

func NewSnapshotter(ctx context.Context, cfg *config.Config) (snapshots.Snapshotter, error) {
	verifier, err := signature.NewVerifier(cfg.PublicKeyFile, cfg.ValidateSignature)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize verifier")
	}

	daemonConfig, err := daemonconfig.NewDaemonConfig(cfg.FsDriver, cfg.DaemonCfgPath)
	if err != nil {
		return nil, errors.Wrap(err, "load daemon configuration")
	}

	db, err := store.NewDatabase(cfg.RootDir)
	if err != nil {
		return nil, errors.Wrap(err, "create database")
	}

	rp, err := manager.ParseRecoverPolicy(cfg.RecoverPolicy)
	if err != nil {
		return nil, errors.Wrap(err, "parse recover policy")
	}

	manager, err := manager.NewManager(manager.Opt{
		NydusdBinaryPath: cfg.NydusdBinaryPath,
		Database:         db,
		DaemonMode:       cfg.DaemonMode,
		CacheDir:         cfg.CacheDir,
		RootDir:          cfg.RootDir,
		RecoverPolicy:    rp,
		DaemonConfig:     daemonConfig,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create daemons manager")
	}

	if cfg.APISocket != "" {
		systemController, err := system.NewSystemController(manager, cfg.APISocket)
		if err != nil {
			return nil, errors.Wrap(err, "create system controller")
		}
		go systemController.Run()
	}

	opts := []fspkg.NewFSOpt{
		fspkg.WithManager(manager),
		fspkg.WithNydusImageBinaryPath(cfg.NydusImageBinaryPath),
		fspkg.WithMeta(cfg.RootDir),
		fspkg.WithVPCRegistry(cfg.ConvertVpcRegistry),
		fspkg.WithVerifier(verifier),
		fspkg.WithDaemonMode(cfg.DaemonMode),
		fspkg.WithFsDriver(cfg.FsDriver),
		fspkg.WithLogLevel(cfg.LogLevel),
		fspkg.WithLogDir(cfg.LogDir),
		fspkg.WithLogToStdout(cfg.LogToStdout),
		fspkg.WithRootMountpoint(path.Join(cfg.RootDir, "mnt")),
		fspkg.WithNydusdThreadNum(cfg.NydusdThreadNum),
		fspkg.WithEnableStargz(cfg.EnableStargz),
	}

	if !cfg.DisableCacheManager {
		cacheMgr, err := cache.NewManager(cache.Opt{
			Database: db,
			Period:   cfg.GCPeriod,
			CacheDir: cfg.CacheDir,
			FsDriver: cfg.FsDriver,
		})
		if err != nil {
			return nil, errors.Wrap(err, "create cache manager")
		}
		opts = append(opts, fspkg.WithCacheManager(cacheMgr))
	}

	// Prefetch mode counts as no daemon, as daemon is only for prefetch,
	// container rootfs doesn't need daemon
	hasDaemon := cfg.DaemonMode != config.DaemonModeNone

	nydusFs, err := fspkg.NewFileSystem(ctx, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize nydus filesystem")
	}

	// With fuse driver enabled and a fuse daemon configuration with "localfs"
	// storage backend, it indicates that a Blobs Manager is needed to download
	// blobs from registry alone with no help of nydusd or containerd.
	var blobMgr *blob.Manager
	if fuseConfig, ok := daemonConfig.(*daemonconfig.FuseDaemonConfig); ok {
		if fuseConfig.Device.Backend.BackendType == "localfs" &&
			len(fuseConfig.Device.Backend.Config.Dir) != 0 {
			blobMgr = blob.NewBlobManager(fuseConfig.Device.Backend.Config.Dir, resolve.NewResolver())
			go func() {
				err := blobMgr.Run(ctx)
				if err != nil {
					log.G(ctx).Warnf("blob manager run failed %s", err)
				}
			}()
		}
	}

	if cfg.EnableMetrics {
		metricServer, err := metrics.NewServer(
			ctx,
			metrics.WithRootDir(cfg.RootDir),
			metrics.WithMetricsFile(cfg.MetricsFile),
			metrics.WithProcessManager(manager),
		)
		if err != nil {
			return nil, errors.Wrap(err, "create metrics server")
		}
		// Start metrics http server.
		go func() {
			if err := metricServer.Serve(ctx); err != nil {
				log.L.Errorf("Failed to start metrics server, %s", err)
			}
		}()
	}

	if err := os.MkdirAll(cfg.RootDir, 0700); err != nil {
		return nil, err
	}

	supportsDType, err := getSupportsDType(cfg.RootDir)
	if err != nil {
		return nil, err
	}
	if !supportsDType {
		return nil, fmt.Errorf("%s does not support d_type. If the backing filesystem is xfs, please reformat with ftype=1 to enable d_type support", cfg.RootDir)
	}

	ms, err := storage.NewMetaStore(filepath.Join(cfg.RootDir, "metadata.db"))
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(cfg.RootDir, "snapshots"), 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}

	return &snapshotter{
		root:                 cfg.RootDir,
		nydusdPath:           cfg.NydusdBinaryPath,
		ms:                   ms,
		syncRemove:           cfg.SyncRemove,
		fs:                   nydusFs,
		manager:              manager,
		hasDaemon:            hasDaemon,
		enableNydusOverlayFS: cfg.EnableNydusOverlayFS,
		cleanupOnClose:       cfg.CleanupOnClose,
		blobMgr:              blobMgr,
	}, nil
}

func (o *snapshotter) Cleanup(ctx context.Context) error {
	cleanup, err := o.cleanupDirectories(ctx)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("cleanup: dirs=%v", cleanup)
	for _, dir := range cleanup {
		if err := o.cleanupSnapshotDirectory(ctx, dir); err != nil {
			log.G(ctx).WithError(err).WithField("path", dir).Warn("failed to remove directory")
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

	upperPath := o.upperPath(id)

	if info.Kind == snapshots.KindActive {
		du, err := fs.DiskUsage(ctx, upperPath)
		if err != nil {
			return snapshots.Usage{}, err
		}
		usage = snapshots.Usage(du)
	}

	// Blob layers are all committed snapshots
	if info.Kind == snapshots.KindCommitted {
		blobDigest := info.Labels[label.CRILayerDigest]
		// Try to get nydus meta layer/snapshot disk usage
		cacheUsage, err := o.fs.CacheUsage(ctx, blobDigest)
		if err != nil {
			return snapshots.Usage{}, errors.Wrapf(err, "try to get snapshot %s nydus disk usage", id)
		}
		usage.Add(cacheUsage)
	}

	return usage, nil
}

func (o *snapshotter) getSnapShot(ctx context.Context, key string) (*storage.Snapshot, error) {
	return snapshot.GetSnapshot(ctx, o.ms, key)
}

func (o *snapshotter) Mounts(ctx context.Context, key string) ([]mount.Mount, error) {
	log.G(ctx).Infof("mount snapshot with key %s", key)
	s, err := o.getSnapShot(ctx, key)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get active mount")
	}

	if id, _, rErr := o.findMetaLayer(ctx, key); rErr == nil {
		err = o.fs.WaitUntilReady(id)
		if err != nil {
			log.G(ctx).Errorf("snapshot %s is not ready, err: %v", id, err)
			return nil, err
		}
		return o.remoteMounts(ctx, *s, id)
	}

	_, snap, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to get info for snapshot %s", key))
	}

	return o.mounts(ctx, &snap, *s)
}

func (o *snapshotter) prepareRemoteSnapshot(ctx context.Context, id string, labels map[string]string) error {
	log.G(ctx).Infof("prepare remote snapshot mountpoint %s", o.upperPath(id))
	return o.fs.Mount(id, labels)
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	logCtx := log.G(ctx).WithField("key", key).WithField("parent", parent)
	base, s, err := o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
	if err != nil {
		return nil, err
	}
	logCtx.Debugf("prepare snapshot with labels %v", base.Labels)

	// Handle nydus/stargz image data layers.
	if target, ok := base.Labels[label.TargetSnapshotRef]; ok {
		// check if image layer is nydus data layer
		if isNydusDataLayer(base.Labels) {
			logCtx.Infof("nydus data layer, skip download and unpack %s", key)

			if o.blobMgr != nil {
				err = o.blobMgr.PrepareBlobLayer(s, base.Labels)
				if err != nil {
					logCtx.Errorf("failed to prepare nydus data layer of snapshot ID %s, err: %v", s.ID, err)
					return nil, err
				}
			}

			err := o.Commit(ctx, target, key, append(opts, snapshots.WithLabels(base.Labels))...)
			if err == nil || errdefs.IsAlreadyExists(err) {
				return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "target snapshot %q", target)
			}
		} else if !isNydusMetaLayer(base.Labels) {
			// Check if image layer is estargz layer
			if ok, ref, layerDigest, blob := o.fs.IsStargzDataLayer(ctx, base.Labels); ok {
				err = o.fs.PrepareStargzMetaLayer(ctx, blob, ref, layerDigest, s, base.Labels)
				if err != nil {
					logCtx.Errorf("prepare stargz layer of snapshot ID %s, err: %v", s.ID, err)
					// fallback to default OCIv1 handler
				} else {
					// Mark this snapshot as stargz layer
					base.Labels[label.StargzLayer] = "true"
					err := o.Commit(ctx, target, key, append(opts, snapshots.WithLabels(base.Labels))...)
					if err == nil || errdefs.IsAlreadyExists(err) {
						return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "target snapshot %q", target)
					}
				}
			}
		}
	} else {
		// Mount image for running container, which has a nydus/stargz image as parent.
		logCtx.Infof("prepare for container layer %s", key)
		if id, info, err := o.findMetaLayer(ctx, key); err == nil {
			// For stargz layer, we need to merge all bootstraps into one.
			if o.fs.StargzLayer(info.Labels) {
				if err := o.fs.MergeStargzMetaLayer(ctx, s); err != nil {
					return nil, errors.Wrap(err, "merge stargz meta layer")
				}
			}

			logCtx.Infof("found nydus meta layer id %s", id)

			if err := o.prepareRemoteSnapshot(ctx, id, info.Labels); err != nil {
				return nil, err
			}

			return o.remoteMounts(ctx, s, id)
		}
	}

	return o.mounts(ctx, base, s)
}

func (o *snapshotter) findMetaLayer(ctx context.Context, key string) (string, snapshots.Info, error) {
	return snapshot.FindSnapshot(ctx, o.ms, key, func(info snapshots.Info) bool {
		_, ok := info.Labels[label.NydusMetaLayer]
		if !ok && o.fs.StargzEnabled() {
			_, ok = info.Labels[label.StargzLayer]
		}
		return ok
	})
}

func (o *snapshotter) View(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	log.G(ctx).Infof("view snapshot with key %s", key)
	base, s, err := o.createSnapshot(ctx, snapshots.KindView, key, parent, opts)
	if err != nil {
		return nil, err
	}

	return o.mounts(ctx, base, s)
}

func (o *snapshotter) Commit(ctx context.Context, name, key string, opts ...snapshots.Opt) error {
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("failed to rollback transaction")
			}
		}
	}()

	// grab the existing id
	id, _, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return err
	}

	log.G(ctx).Infof("commit snapshot with key %s snapshot id %s", key, id)

	usage, err := fs.DiskUsage(ctx, o.upperPath(id))
	if err != nil {
		return err
	}

	if _, err = storage.CommitActive(ctx, key, name, snapshots.Usage(usage), opts...); err != nil {
		return errors.Wrap(err, "failed to commit snapshot")
	}

	return t.Commit()
}

func (o *snapshotter) Remove(ctx context.Context, key string) error {
	ctx, t, err := o.ms.TransactionContext(ctx, true)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			if rerr := t.Rollback(); rerr != nil {
				log.G(ctx).WithError(rerr).Warn("failed to rollback transaction")
			}
		}
	}()

	id, info, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return errors.Wrap(err, "get snapshot")
	}

	// For example: remove snapshot with key sha256:c33c40022c8f333e7f199cd094bd56758bc479ceabf1e490bb75497bf47c2ebf
	log.L.Infof("remove snapshot with key %s snapshot id %s", key, id)

	if info.Kind == snapshots.KindCommitted {
		blobDigest := info.Labels[label.CRILayerDigest]
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
	var blobDigest string
	_, cleanupBlob := info.Labels[label.NydusDataLayer]
	if cleanupBlob {
		blobDigest = info.Labels[label.CRILayerDigest]
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
				if cleanupBlob && o.blobMgr != nil {
					if err := o.blobMgr.CleanupBlobLayer(ctx, blobDigest, false); err != nil {
						log.G(ctx).WithError(err).WithField("id", key).Warn("failed to remove blob")
					}
				}
			}
		}()
	} else {
		defer func() {
			if cleanupBlob && o.blobMgr != nil {
				if err := o.blobMgr.CleanupBlobLayer(ctx, blobDigest, true); err != nil {
					log.G(ctx).WithError(err).WithField("id", key).Warn("failed to remove blob")
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
	defer t.Rollback()
	return storage.WalkInfo(ctx, fn, fs...)
}

func (o *snapshotter) Close() error {
	if o.cleanupOnClose {
		err := o.fs.Cleanup(context.Background())
		if err != nil {
			log.L.Errorf("failed to clean up remote snapshot, err %v", err)
		}
	}
	return o.ms.Close()
}

func (o *snapshotter) upperPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "fs")
}

func (o *snapshotter) lowerPath(id string) (mnt string, err error) {
	if mnt, err = o.fs.MountPoint(id); err == nil {
		return mnt, nil
	}

	return "", err
}

func (o *snapshotter) workPath(id string) string {
	return filepath.Join(o.root, "snapshots", id, "work")
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

	var td, path string
	defer func() {
		if td != "" {
			if err1 := o.cleanupSnapshotDirectory(ctx, td); err1 != nil {
				log.G(ctx).WithError(err1).Warn("failed to cleanup temp snapshot directory")
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
		return nil, storage.Snapshot{}, errors.Wrap(err, "failed to create prepare snapshot dir")
	}

	s, err := storage.CreateSnapshot(ctx, kind, key, parent, opts...)
	if err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "failed to create snapshot")
	}

	if len(s.ParentIDs) > 0 {
		st, err := os.Stat(o.upperPath(s.ParentIDs[0]))
		if err != nil {
			return nil, storage.Snapshot{}, errors.Wrap(err, "failed to stat parent")
		}

		// FIXME: Why only change owner of having parent?
		if err := lchown(filepath.Join(td, "fs"), st); err != nil {
			return nil, storage.Snapshot{}, errors.Wrap(err, "failed to chown")
		}
	}

	path = o.snapshotDir(s.ID)
	if err = os.Rename(td, path); err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "failed to rename")
	}
	td = ""

	rollback = false
	if err = t.Commit(); err != nil {
		return nil, storage.Snapshot{}, errors.Wrap(err, "commit failed")
	}
	path = ""

	return &base, s, nil
}

func bindMount(source string) []mount.Mount {
	return []mount.Mount{
		{
			Type:   "bind",
			Source: source,
			Options: []string{
				"ro",
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

type ExtraOption struct {
	Source      string `json:"source"`
	Config      string `json:"config"`
	Snapshotdir string `json:"snapshotdir"`
	Version     string `json:"fs_version"`
}

func (o *snapshotter) remoteMounts(ctx context.Context, s storage.Snapshot, id string) ([]mount.Mount, error) {
	var overlayOptions []string
	if s.Kind == snapshots.KindActive {
		overlayOptions = append(overlayOptions,
			fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
		)
	} else if len(s.ParentIDs) == 1 {
		return bindMount(o.upperPath(s.ParentIDs[0])), nil
	}

	lowerPath, err := o.lowerPath(id)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to locate overlay lowerdir")
	}

	lowerDirOption := fmt.Sprintf("lowerdir=%s", lowerPath)
	overlayOptions = append(overlayOptions, lowerDirOption)

	// when hasDaemon and not enableNydusOverlayFS, return overlayfs mount slice
	if !o.enableNydusOverlayFS && o.hasDaemon {
		log.G(ctx).Infof("remote mount options %v", overlayOptions)
		return overlayMount(overlayOptions), nil
	}

	source, err := o.fs.BootstrapFile(id)
	if err != nil {
		return nil, err
	}

	instance := daemon.RafsSet.Get(id)
	// TODO: How to dump configuration if no daemon is ever created?
	daemon := o.fs.Manager.GetByDaemonID(instance.DaemonID)

	configContent, err := daemon.Config.DumpString()
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal config")
	}

	// get version from bootstrap
	f, err := os.Open(source)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: check bootstrap version: failed to open bootstrap")
	}
	defer f.Close()

	header := make([]byte, 4096)
	sz, err := f.Read(header)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: check bootstrap version: failed to read bootstrap")
	}
	version, err := layout.DetectFsVersion(header[0:sz])
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to detect filesystem version")
	}
	// when enable nydus-overlayfs, return unified mount slice for runc and kata
	extraOption := &ExtraOption{
		Source:      source,
		Config:      configContent,
		Snapshotdir: o.snapshotDir(s.ID),
		Version:     version,
	}
	no, err := json.Marshal(extraOption)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal NydusOption")
	}
	// XXX: Log options without extraoptions as it might contain secrets.
	log.G(ctx).Debugf("fuse.nydus-overlayfs mount options %v", overlayOptions)
	// base64 to filter easily in `nydus-overlayfs`
	opt := fmt.Sprintf("extraoption=%s", base64.StdEncoding.EncodeToString(no))
	overlayOptions = append(overlayOptions, opt)
	return []mount.Mount{
		{
			Type:    "fuse.nydus-overlayfs",
			Source:  "overlay",
			Options: overlayOptions,
		},
	}, nil
}

func (o *snapshotter) mounts(ctx context.Context, info *snapshots.Info, s storage.Snapshot) ([]mount.Mount, error) {
	if len(s.ParentIDs) == 0 {
		// if we only have one layer/no parents then just return a bind mount as overlay will not work
		roFlag := "rw"
		if s.Kind == snapshots.KindView {
			roFlag = "ro"
		}

		return []mount.Mount{
			{
				Source: o.upperPath(s.ID),
				Type:   "bind",
				Options: []string{
					roFlag,
					"rbind",
				},
			},
		}, nil
	}

	var options []string
	if s.Kind == snapshots.KindActive {
		options = append(options,
			fmt.Sprintf("workdir=%s", o.workPath(s.ID)),
			fmt.Sprintf("upperdir=%s", o.upperPath(s.ID)),
		)
		if _, ok := info.Labels[label.OverlayfsVolatileOpt]; ok {
			options = append(options, "volatile")
		}
	} else if len(s.ParentIDs) == 1 {
		return []mount.Mount{
			{
				Source: o.upperPath(s.ParentIDs[0]),
				Type:   "bind",
				Options: []string{
					"ro",
					"rbind",
				},
			},
		}, nil
	}

	parentPaths := make([]string, len(s.ParentIDs))
	for i := range s.ParentIDs {
		parentPaths[i] = o.upperPath(s.ParentIDs[i])
	}
	options = append(options, fmt.Sprintf("lowerdir=%s", strings.Join(parentPaths, ":")))

	log.G(ctx).Debugf("overlayfs mount options %s", options)
	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}, nil
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

	var cleanup []string
	for _, d := range dirs {
		if _, ok := ids[d]; ok {
			continue
		}
		// When it quits, there will be nothing inside
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
	defer t.Rollback()
	return o.getCleanupDirectories(ctx)
}

func (o *snapshotter) cleanupSnapshotDirectory(ctx context.Context, dir string) error {
	// On a remote snapshot, the layer is mounted on the "mnt" directory.
	// We use Filesystem's Unmount API so that it can do necessary finalization
	// before/after the unmount.

	// For example: cleanupSnapshotDirectory /var/lib/containerd-nydus/snapshots/34" dir=/var/lib/containerd-nydus/snapshots/34
	log.L.Infof("cleanupSnapshotDirectory %s", dir)

	if err := o.fs.Umount(ctx, dir); err != nil && !os.IsNotExist(err) {
		log.G(ctx).WithError(err).WithField("dir", dir).Error("failed to unmount")
	}

	if err := os.RemoveAll(dir); err != nil {
		return errors.Wrapf(err, "failed to remove directory %q", dir)
	}

	log.L.Infof("Delete snapshot directory %s", dir)

	return nil
}

func (o *snapshotter) snapshotRoot() string {
	return filepath.Join(o.root, "snapshots")
}

func (o *snapshotter) snapshotDir(id string) string {
	return filepath.Join(o.snapshotRoot(), id)
}

func getSupportsDType(dir string) (bool, error) {
	return fs.SupportsDType(dir)
}

func lchown(target string, st os.FileInfo) error {
	stat := st.Sys().(*syscall.Stat_t)
	return os.Lchown(target, int(stat.Uid), int(stat.Gid))
}
