/*
/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
*/

package snapshot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/continuity/fs"
	"github.com/containerd/nydus-snapshotter/converter"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	nydusErrdefs "github.com/containerd/nydus-snapshotter/pkg/errdefs"
	metrics "github.com/containerd/nydus-snapshotter/pkg/metric"
	"github.com/containerd/nydus-snapshotter/pkg/store"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/opencontainers/go-digest"
	"github.com/pingcap/failpoint"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	fspkg "github.com/containerd/nydus-snapshotter/pkg/filesystem/fs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/process"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/snapshot"
	// Import the converter package so that it can be compiled during
	// `go build` to ensure cross-compilation compatibility.
)

var _ snapshots.Snapshotter = &snapshotter{}

type snapshotter struct {
	context              context.Context
	root                 string
	nydusdPath           string
	ms                   *storage.MetaStore
	fs                   *fspkg.Filesystem
	manager              *process.Manager
	hasDaemon            bool
	enableNydusOverlayFS bool
	syncRemove           bool
	cleanupOnClose       bool
	converter            converter.Converter
}

func NewSnapshotter(ctx context.Context, cfg *config.Config) (snapshots.Snapshotter, error) {
	verifier, err := signature.NewVerifier(cfg.PublicKeyFile, cfg.ValidateSignature)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize verifier")
	}

	cfg.DaemonMode = strings.ToLower(cfg.DaemonMode)
	if cfg.DaemonMode == config.DaemonModePrefetch && !cfg.DaemonCfg.FSPrefetch.Enable {
		log.G(ctx).Warnf("Daemon mode is %s but fs_prefetch is not enabled, change to %s mode", cfg.DaemonMode, config.DaemonModeNone)
		cfg.DaemonMode = config.DaemonModeNone
	}

	db, err := store.NewDatabase(cfg.RootDir)
	if err != nil {
		return nil, errors.Wrap(err, "failed to new database")
	}

	pm, err := process.NewManager(process.Opt{
		NydusdBinaryPath: cfg.NydusdBinaryPath,
		Database:         db,
		DaemonMode:       cfg.DaemonMode,
		CacheDir:         cfg.CacheDir,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to new process manager")
	}

	opts := []fspkg.NewFSOpt{
		fspkg.WithProcessManager(pm),
		fspkg.WithNydusdBinaryPath(cfg.NydusdBinaryPath, cfg.DaemonMode),
		fspkg.WithNydusImageBinaryPath(cfg.NydusImageBinaryPath),
		fspkg.WithMeta(cfg.RootDir),
		fspkg.WithDaemonConfig(cfg.DaemonCfg),
		fspkg.WithVPCRegistry(cfg.ConvertVpcRegistry),
		fspkg.WithVerifier(verifier),
		fspkg.WithDaemonMode(cfg.DaemonMode),
		fspkg.WithFsDriver(cfg.FsDriver),
		fspkg.WithLogLevel(cfg.LogLevel),
		fspkg.WithLogDir(cfg.LogDir),
		fspkg.WithLogToStdout(cfg.LogToStdout),
		fspkg.WithNydusdThreadNum(cfg.NydusdThreadNum),
		fspkg.WithImageMode(cfg.DaemonCfg),
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
			return nil, errors.Wrap(err, "failed to new cache manager")
		}
		opts = append(opts, fspkg.WithCacheManager(cacheMgr))
	}

	// Prefetch mode counts as no daemon, as daemon is only for prefetch,
	// container rootfs doesn't need daemon
	hasDaemon := cfg.DaemonMode != config.DaemonModeNone && cfg.DaemonMode != config.DaemonModePrefetch

	nydusFs, err := fspkg.NewFileSystem(ctx, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize nydus filesystem")
	}

	if cfg.EnableMetrics {
		metricServer, err := metrics.NewServer(
			ctx,
			metrics.WithRootDir(cfg.RootDir),
			metrics.WithMetricsFile(cfg.MetricsFile),
			metrics.WithProcessManager(pm),
		)
		if err != nil {
			return nil, errors.Wrap(err, "failed to new metric server")
		}
		// Start metrics http server.
		go func() {
			if err := metricServer.Serve(ctx); err != nil {
				log.G(ctx).Error(err)
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

	var converterConfig *converter.Config
	var conv converter.Converter
	if cfg.ContainerdAddress != "" {
		if nydusFs.ImageMode == fspkg.PreLoad {
			timeout := cfg.ConverterTimeout
			if timeout == 0 {
				timeout = converter.MaxTimeOut
			}
			converterConfig = &converter.Config{
				ContainerdAddress: cfg.ContainerdAddress,
				BlobDir:           cfg.DaemonCfg.Device.Backend.Config.Dir,
				BuilderPath:       cfg.NydusImageBinaryPath,
				Timeout:           &timeout,
			}
			conv, err = converter.New(converterConfig)
			if err != nil {
				// We don't return error on here, it will be initialized again later.
				log.G(ctx).Warnf("failed to init converter, will be initialized again later")
			}
		} else {
			log.G(ctx).Warnf("failed to init converter, which can only be enabled with localfs backend")
		}
	}

	return &snapshotter{
		context:              ctx,
		root:                 cfg.RootDir,
		nydusdPath:           cfg.NydusdBinaryPath,
		ms:                   ms,
		syncRemove:           cfg.SyncRemove,
		fs:                   nydusFs,
		manager:              pm,
		hasDaemon:            hasDaemon,
		enableNydusOverlayFS: cfg.EnableNydusOverlayFS,
		cleanupOnClose:       cfg.CleanupOnClose,
		converter:            conv,
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

	_, snap, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to get info for snapshot %s", key))
	}

	if id, info, rErr := o.findMetaLayer(ctx, key); rErr == nil {
		err = o.fs.WaitUntilReady(ctx, id)
		if err != nil {
			log.G(ctx).Errorf("snapshot %s is not ready, err: %v", id, err)
			return nil, err
		}
		return o.remoteMounts(ctx, *s, id, info.Labels)
	} else if o.converter != nil {
		if len(s.ParentIDs) == 0 {
			return o.mounts(ctx, &snap, *s)
		}
		cKey := snap.Parent
		pid, pinfo, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, cKey)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to get parent %s snapshot info of snapshot: %#v", cKey, snap)
		}
		bootstrap := filepath.Join(o.fs.UpperPath(pid), fspkg.BootstrapFile)
		if _, err := os.Stat(bootstrap); err != nil {
			return o.mounts(ctx, &snap, *s)
		}
		return o.remoteMounts(ctx, *s, pid, pinfo.Labels)
	}

	return o.mounts(ctx, &snap, *s)
}

func (o *snapshotter) prepareRemoteSnapshot(ctx context.Context, id string, labels map[string]string) error {
	log.G(ctx).Infof("prepare remote snapshot mountpoint %s", o.upperPath(id))
	return o.fs.Mount(o.context, id, labels)
}

func (o *snapshotter) Prepare(ctx context.Context, key, parent string, opts ...snapshots.Opt) ([]mount.Mount, error) {
	logCtx := log.G(ctx).WithField("key", key).WithField("parent", parent)
	base, s, err := o.createSnapshot(ctx, snapshots.KindActive, key, parent, opts)
	if err != nil {
		return nil, err
	}
	logCtx.Infof("prepare snapshot with labels %v", base.Labels)

	// Handle nydus/stargz image data layers.
	if target, ok := base.Labels[label.TargetSnapshotRef]; ok {
		logCtx.Infof("snapshot ref label found %s", label.TargetSnapshotRef)
		// check if image layer is nydus data layer
		if o.fs.IsNydusDataLayer(ctx, base.Labels) {
			logCtx.Infof("nydus data layer, skip download and unpack %s", key)
			err = o.fs.PrepareBlobLayer(ctx, s, base.Labels)
			if err != nil {
				logCtx.Errorf("failed to prepare nydus data layer of snapshot ID %s, err: %v", s.ID, err)
				return nil, err
			}

			err := o.Commit(ctx, target, key, append(opts, snapshots.WithLabels(base.Labels))...)
			if err == nil || errdefs.IsAlreadyExists(err) {
				return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "target snapshot %q", target)
			}
		} else if !o.fs.IsNydusMetaLayer(ctx, base.Labels) {
			if ok, ref, layerDigest, blob := o.fs.IsStargzDataLayer(ctx, base.Labels); ok {
				// Check if image layer is estargz layer
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
			} else if o.converter != nil {
				logCtx.Infof("start to prepare oci to nydus layer with labels %#+v", base.Labels)
				err = o.prepareOCItoNydusLayer(ctx, base.Labels, target)
				failpoint.Inject("converter-afetr-failed", func() ([]mount.Mount, error) {
					return nil, fmt.Errorf("convert failed")
				})
				if err != nil {
					if !nydusErrdefs.IsMissingLabels(err) {
						return nil, errors.Wrapf(err, "failed to prepare oci to nydus layer of snapshot ID %s", s.ID)
					}
					logCtx.Warnf("missing cri labels in oci layer with snapshot ID %s, err: %v", s.ID, err)
				} else {
					err := o.Commit(ctx, target, key, append(opts, snapshots.WithLabels(base.Labels))...)
					if err == nil || errdefs.IsAlreadyExists(err) {
						return nil, errors.Wrapf(errdefs.ErrAlreadyExists, "target snapshot %q", target)
					}
				}
			}
		}

	}

	// Mount image for running container, which has a nydus/stargz image as parent.
	if prepareForContainer(*base) {
		logCtx.Infof("prepare for container layer %s", key)
		if id, info, err := o.findMetaLayer(ctx, key); err == nil {
			// For stargz layer, we need to merge all bootstraps into one.
			if o.fs.StargzLayer(info.Labels) {
				if err := o.fs.MergeStargzMetaLayer(ctx, s); err != nil {
					return nil, errors.Wrap(err, "merge stargz meta layer")
				}
			}

			logCtx.Infof("found nydus meta layer id %s, parpare remote snapshot", id)
			imageID, _, _, _ := registry.ParseLabels(info.Labels)
			blobsIDs, err := o.fs.GetBlobIDs(info.Labels)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get nydus blob ids")
			}
			if err := o.fs.AddSnapshot(imageID, blobsIDs); err != nil {
				return nil, errors.Wrap(err, "cache manager failed to add snapshot")
			}

			return o.remoteSnapshotMounts(ctx, s, id, info.Labels)
		} else if o.converter != nil {
			if len(s.ParentIDs) == 0 {
				return o.mounts(ctx, base, s)
			}
			logCtx.Infof("failed to find meta layer label in snapshots, start to check whether there are blobs converted from the OCI layer named with the snapshot key")
			_, snap, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, key)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get snapshot info")
			}
			cKey := snap.Parent
			pid, pinfo, _, err := snapshot.GetSnapshotInfo(ctx, o.ms, cKey)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get parent snapshot info")
			}

			// get blobs from the last snapshot (corresponding to the top layer of the OCI image)
			// to the first snapshot (corresponding to the bottom layer of the OCI image),
			// therefore, it should be traversed in reverse order in Merge().
			blobs, err := getBlobs(ctx, o.ms, o.converter.BlobDir(), cKey)
			if err != nil {
				logCtx.Infof("failed to find blobs converted from the OCI layer named with the snapshot key %v", err)
				return o.mounts(ctx, base, s)
			}
			logCtx.Infof("find blobs converted from the OCI layer: %v", blobs)
			imageID, manifest, _, _ := registry.ParseLabels(pinfo.Labels)
			manifestDigest, err := digest.Parse(manifest)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to parse manifest digest when cleaning ManifestLayersMap, label %#v, manifest %s,", pinfo.Labels, manifest)
			}
			o.converter.DeleteImage(imageID)
			o.converter.DeleteManifest(manifestDigest)
			if err := o.fs.AddSnapshot(imageID, blobs); err != nil {
				return nil, errors.Wrap(err, "cache manager failed to add snapshot")
			}

			bootstrapPath := filepath.Join(o.fs.UpperPath(pid), fspkg.BootstrapFile)
			if _, err := os.Stat(bootstrapPath); err == nil {
				logCtx.Infof("bootstrap already exists %s", bootstrapPath)
				return o.remoteSnapshotMounts(ctx, s, pid, pinfo.Labels)
			}

			err = os.Mkdir(filepath.Dir(bootstrapPath), 0600)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create bootstrap dir")
			}

			if err := o.converter.Merge(ctx, blobs, bootstrapPath); err != nil {
				return nil, errors.Wrap(err, "failed to merge blobs into final bootstrap")
			}
			logCtx.Infof("merge uncompressed bootstrap successfully")

			return o.remoteSnapshotMounts(ctx, s, pid, pinfo.Labels)
		}
	}

	return o.mounts(ctx, base, s)
}

func (o *snapshotter) remoteSnapshotMounts(ctx context.Context, s storage.Snapshot, id string, labels map[string]string) ([]mount.Mount, error) {
	if o.manager.IsPrefetchDaemon() {
		// Prepare prefetch mount in background, so we could return Mounts
		// info to containerd as soon as possible.
		go func() {
			log.G(ctx).Infof("Prepare prefetch daemon for id %s", id)
			err := o.prepareRemoteSnapshot(ctx, id, labels)
			// failure of prefetch mount is not fatal, just print a warning
			if err != nil {
				log.G(ctx).WithError(err).Warnf("Prepare prefetch mount failed for id %s", id)
			}
		}()
	} else if err := o.prepareRemoteSnapshot(ctx, id, labels); err != nil {
		return nil, err
	}
	return o.remoteMounts(ctx, s, id, labels)
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

func prepareForContainer(info snapshots.Info) bool {
	_, ok := info.Labels[label.CRIImageLayers]
	return !ok
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
	log.G(ctx).Infof("commit snapshot with key %s", key)
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
	log.G(ctx).Infof("remove snapshot with key %s", key)
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

	_, snap, _, err := storage.GetInfo(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to get snapshot")
	}

	if snap.Labels != nil {
		if imageID, ok := snap.Labels[label.CRIImageRef]; ok {
			if err := o.fs.DelSnapshot(imageID); err != nil {
				return errors.Wrap(err, "failed to delete snapshot in cache manager")
			}
		} else {
			return fmt.Errorf("failed to get image ref from snapshot label %#v", snap.Labels)
		}
	}

	_, _, err = storage.Remove(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to remove")
	}

	var blobDigest string
	var cleanupBlob bool
	if blobDigest, cleanupBlob = snap.Labels[label.NydusConvertedLayer]; !cleanupBlob {
		if _, cleanupBlob = snap.Labels[label.NydusDataLayer]; cleanupBlob {
			blobDigest = snap.Labels[label.CRILayerDigest]
		}
	}

	if o.syncRemove {
		var removals []string
		removals, err = o.getCleanupDirectories(ctx)
		if err != nil {
			return errors.Wrap(err, "unable to get directories for removal")
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
				if cleanupBlob {
					if err := o.fs.CleanupBlobLayer(ctx, blobDigest, false); err != nil {
						log.G(ctx).WithError(err).WithField("id", key).Warn("failed to remove blob")
					}
				}
			}
		}()
	} else {
		defer func() {
			if cleanupBlob {
				if err := o.fs.CleanupBlobLayer(ctx, blobDigest, true); err != nil {
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

	td, err = o.prepareDirectory(ctx, o.snapshotRoot(), kind)
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

func (o *snapshotter) remoteMounts(ctx context.Context, s storage.Snapshot, id string, labels map[string]string) ([]mount.Mount, error) {
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

	cfg, err := o.fs.NewDaemonConfig(labels, id)
	if err != nil {
		return nil, errors.Wrapf(err, fmt.Sprintf("remoteMounts: failed to generate nydus config for snapshot %s, label: %v", id, labels))
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal config")
	}
	configContent := string(b)
	// We already Marshal config and save it in configContent, reset Auth and
	// RegistryToken so it could be printed and to make debug easier
	cfg.Device.Backend.Config.Auth = ""
	cfg.Device.Backend.Config.RegistryToken = ""
	b, err = json.Marshal(cfg)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal config")
	}
	log.G(ctx).Infof("Bootstrap file for snapshotID %s: %s, config %s", id, source, string(b))

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
	version, err := fspkg.DetectFsVersion(header[0:sz])
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
	log.G(ctx).Infof("fuse.nydus-overlayfs mount options %v", overlayOptions)
	// base64 to filter easily in `nydus-overlayfs`
	opt := fmt.Sprintf("extraoption=%s", base64.StdEncoding.EncodeToString(no))
	options := append(overlayOptions, opt)
	return []mount.Mount{
		{
			Type:    "fuse.nydus-overlayfs",
			Source:  "overlay",
			Options: options,
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

	log.G(ctx).Infof("overlayfs mount options %s", options)
	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: options,
		},
	}, nil
}

func (o *snapshotter) prepareDirectory(ctx context.Context, snapshotDir string, kind snapshots.Kind) (string, error) {
	td, err := ioutil.TempDir(snapshotDir, "new-")
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
	// On a remote snapshot, the layer is mounted on the "fs" directory.
	// We use Filesystem's Unmount API so that it can do necessary finalization
	// before/after the unmount.
	log.G(ctx).WithField("dir", dir).Infof("cleanupSnapshotDirectory %s", dir)
	if err := o.fs.Umount(ctx, dir); err != nil && !os.IsNotExist(err) {
		log.G(ctx).WithError(err).WithField("dir", dir).Error("failed to unmount")
	}

	if err := os.RemoveAll(dir); err != nil {
		return errors.Wrapf(err, "failed to remove directory %q", dir)
	}
	return nil
}

func (o *snapshotter) snapshotRoot() string {
	return filepath.Join(o.root, "snapshots")
}

func (o *snapshotter) snapshotDir(id string) string {
	return filepath.Join(o.snapshotRoot(), id)
}

func (o *snapshotter) prepareOCItoNydusLayer(ctx context.Context, labels map[string]string, target string) error {
	source, manifest, layer, _ := registry.ParseLabels(labels)
	if source == "" || manifest == "" || layer == "" {
		return errors.Wrapf(nydusErrdefs.ErrMissingLabels, "failed to parse manifest digest, source %s, manifest %s, layer %s", source, manifest, layer)
	}
	manifestDigest, err := digest.Parse(manifest)
	if err != nil {
		return errors.Wrap(err, "failed to parse manifest digest")
	}
	layerDigest, err := digest.Parse(layer)
	if err != nil {
		return errors.Wrap(err, "failed to parse current layer digest")
	}
	keyDigest, err := digest.Parse(target)
	if err != nil {
		return errors.Wrap(err, "failed to parse target")
	}
	labels[label.NydusConvertedLayer] = keyDigest.String()
	log.G(ctx).Infof("add (%s, %s) into labels", label.NydusConvertedLayer, labels[label.NydusConvertedLayer])
	blobName := keyDigest.Encoded()

	blobPath := filepath.Join(o.converter.BlobDir(), blobName)
	if _, err := os.Stat(blobPath); err == nil {
		log.G(ctx).Infof("blob already exists %s", blobPath)
		return nil
	}
	failpoint.Inject("convert-oci-to-nydus-failed", func() error {
		return fmt.Errorf("failed to convert OCI layers to nydus blobs")
	})
	if err := o.converter.Convert(context.Background(), source, manifestDigest, layerDigest, blobPath); err == nil {
		labels[label.NydusConvertedLayer] = keyDigest.String()
		log.G(ctx).Infof("convert OCI layers to nydus blobs successfully")
		return nil
	}
	return errors.Wrap(err, "failed to convert OCI layers to nydus blobs")
}

func getBlobs(ctx context.Context, ms *storage.MetaStore, blobdir string, key string) ([]string, error) {
	ctx, t, err := ms.TransactionContext(ctx, false)
	if err != nil {
		return nil, err
	}
	defer t.Rollback()

	var blobs []string
	for cKey := key; cKey != ""; {
		_, info, _, err := storage.GetInfo(ctx, cKey)
		if err != nil {
			log.G(ctx).WithError(err).Warnf("failed to get info of %q", cKey)
			return nil, err
		}
		keyDigest, err := digest.Parse(info.Name)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse info name")
		}
		blob := keyDigest.Encoded()
		if _, err := os.Stat(filepath.Join(blobdir, blob)); err != nil {
			return nil, err
		}
		blobs = append(blobs, blob)

		cKey = info.Parent
	}
	return blobs, nil
}
