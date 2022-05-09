/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package nydus

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	fspkg "github.com/containerd/nydus-snapshotter/pkg/filesystem/fs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/process"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

// TODO: Move snapshotter needed all image annotations to nydus-snapshotter.
const LayerAnnotationNydusBlobIDs = "containerd.io/snapshot/nydus-blob-ids"

// RafsV6 layout: 1k + SuperBlock(128) + SuperBlockExtener(256)
// RafsV5 layout: 8K superblock
// So we only need to read the MaxSuperBlockSize size to include both v5 and v6 superblocks
const MaxSuperBlockSize = 8 * 1024
const RafsV6Magic = 0xE0F5E1E2
const ChunkInfoOffset = 1024 + 128 + 24
const RafsV6SuppeOffset = 1024
const BootstrapFile = "image/image.boot"

var nativeEndian binary.ByteOrder

func init() {
	buf := [2]byte{}
	*(*uint16)(unsafe.Pointer(&buf[0])) = uint16(0xABCD)

	switch buf {
	case [2]byte{0xCD, 0xAB}:
		nativeEndian = binary.LittleEndian
	case [2]byte{0xAB, 0xCD}:
		nativeEndian = binary.BigEndian
	default:
		panic("Could not determine native endianness.")
	}
}

type filesystem struct {
	meta.FileSystemMeta
	manager          *process.Manager
	cacheMgr         *cache.Manager
	verifier         *signature.Verifier
	sharedDaemon     *daemon.Daemon
	daemonCfg        config.DaemonConfig
	resolver         *Resolver
	vpcRegistry      bool
	nydusdBinaryPath string
	mode             fspkg.Mode
	logLevel         string
	logDir           string
	logToStdout      bool
	nydusdThreadNum  int
}

// NewFileSystem initialize Filesystem instance
func NewFileSystem(ctx context.Context, opt ...NewFSOpt) (fspkg.FileSystem, error) {
	var fs filesystem
	for _, o := range opt {
		err := o(&fs)
		if err != nil {
			return nil, err
		}
	}
	fs.resolver = NewResolver()

	// Try to reconnect to running daemons
	if err := fs.manager.Reconnect(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to reconnect daemons")
	}
	return &fs, nil
}

func (fs *filesystem) newSharedDaemon() (*daemon.Daemon, error) {
	modeOpt := daemon.WithSharedDaemon()
	if fs.mode == fspkg.PrefetchInstance {
		modeOpt = daemon.WithPrefetchDaemon()
	}

	d, err := daemon.NewDaemon(
		daemon.WithID(daemon.SharedNydusDaemonID),
		daemon.WithSnapshotID(daemon.SharedNydusDaemonID),
		daemon.WithSocketDir(fs.SocketRoot()),
		daemon.WithSnapshotDir(fs.SnapshotRoot()),
		daemon.WithLogDir(fs.logDir),
		daemon.WithRootMountPoint(filepath.Join(fs.RootDir, "mnt")),
		daemon.WithLogLevel(fs.logLevel),
		daemon.WithLogToStdout(fs.logToStdout),
		daemon.WithNydusdThreadNum(fs.nydusdThreadNum),
		modeOpt,
	)
	if err != nil {
		return nil, err
	}
	if err := fs.manager.NewDaemon(d); err != nil {
		return nil, err
	}
	return d, nil
}

func (fs *filesystem) Support(ctx context.Context, labels map[string]string) bool {
	_, dataOk := labels[label.NydusDataLayer]
	_, metaOk := labels[label.NydusMetaLayer]
	return dataOk || metaOk
}

func isRafsV6(buf []byte) bool {
	return nativeEndian.Uint32(buf[RafsV6SuppeOffset:]) == RafsV6Magic
}

func getBootstrapRealSizeInV6(buf []byte) uint64 {
	return nativeEndian.Uint64(buf[ChunkInfoOffset:])
}

func writeBootstrapToFile(reader io.Reader, bootstrap *os.File) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("read bootstrap duration %d", duration.Milliseconds())
	}()
	rd, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	found := false
	tr := tar.NewReader(rd)
	for {
		h, err := tr.Next()
		if err != nil {
			return err
		}
		if h.Name == BootstrapFile {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("not found file image.boot in targz")
	}
	buf := make([]byte, MaxSuperBlockSize)
	_, err = tr.Read(buf)
	if err != nil {
		return err
	}
	_, err = bootstrap.Write(buf)

	if err != nil {
		return err
	}

	if isRafsV6(buf) {
		size := getBootstrapRealSizeInV6(buf)
		if size < MaxSuperBlockSize {
			return fmt.Errorf("invalid chunk info offset %d", size)
		}
		// The content of the chunkinfo part is not needed in the v6 format, so it is discarded here.
		_, err := io.CopyN(bootstrap, tr, int64(size-MaxSuperBlockSize))
		return err
	}

	// Copy remain data to bootstrap file
	_, err = io.Copy(bootstrap, tr)
	return err
}

func (fs *filesystem) PrepareLayer(ctx context.Context, s storage.Snapshot, labels map[string]string) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.G(ctx).Infof("total nydus prepare layer duration %d", duration.Milliseconds())
	}()
	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return fmt.Errorf("can not find ref and digest from label %+v", labels)
	}
	keychain := auth.FromLabels(labels)
	readerCloser, err := fs.resolver.Resolve(ref, layerDigest, keychain)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve from ref %s, digest %s", ref, layerDigest)
	}
	defer func() {
		readerCloser.Close()
	}()

	workdir := filepath.Join(fs.UpperPath(s.ID), BootstrapFile)

	err = os.Mkdir(filepath.Dir(workdir), 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create bootstrap dir")
	}
	nydusBootstrap, err := os.OpenFile(workdir, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create bootstrap file")
	}
	return writeBootstrapToFile(readerCloser, nydusBootstrap)
}

// Mount will be called when containerd snapshotter prepare remote snapshotter
// this method will fork nydus daemon and manage it in the internal store, and indexed by snapshotID
func (fs *filesystem) Mount(ctx context.Context, snapshotID string, labels map[string]string) (err error) {
	// If NoneDaemon mode, we don't mount nydus on host
	if fs.mode == fspkg.NoneInstance {
		return nil
	}

	imageID, ok := labels[label.ImageRef]
	if !ok {
		return fmt.Errorf("failed to find image ref of snapshot %s, labels %v", snapshotID, labels)
	}
	d, err := fs.newDaemon(ctx, snapshotID, imageID)
	// if daemon already exists for snapshotID, just return
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	defer func() {
		if err != nil {
			_ = fs.manager.DestroyDaemon(d)
		}
	}()
	// if publicKey is not empty we should verify bootstrap file of image
	bootstrap, err := d.BootstrapFile()
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to find bootstrap file of daemon %s", d.ID))
	}
	err = fs.verifier.Verify(labels, bootstrap)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to verify signature of daemon %s", d.ID))
	}
	err = fs.mount(d, labels)
	if err != nil {
		log.G(ctx).Errorf("failed to mount %s, %v", d.MountPoint(), err)
		return errors.Wrap(err, fmt.Sprintf("failed to mount daemon %s", d.ID))
	}
	return nil
}

// WaitUntilReady wait until daemon ready by snapshotID, it will wait until nydus domain socket established
// and the status of nydusd daemon must be ready
func (fs *filesystem) WaitUntilReady(ctx context.Context, snapshotID string) error {
	// If NoneDaemon mode, there's no need to wait for daemon ready
	if !fs.hasDaemon() {
		return nil
	}

	d, err := fs.manager.GetBySnapshotID(snapshotID)
	if err != nil {
		return err
	}

	return d.WaitUntilReady()
}

func (fs *filesystem) Umount(ctx context.Context, mountPoint string) error {
	if fs.mode == fspkg.NoneInstance {
		return nil
	}

	id := filepath.Base(mountPoint)
	daemon, err := fs.manager.GetBySnapshotID(id)
	if err != nil {
		return err
	}
	if err := fs.manager.DestroyDaemon(daemon); err != nil {
		return errors.Wrap(err, "destroy daemon err")
	}

	if fs.cacheMgr != nil {
		if err := fs.cacheMgr.DelSnapshot(daemon.ImageID); err != nil {
			return errors.Wrap(err, "del snapshot err")
		}
		log.L.Debugf("remove snapshot %s\n", daemon.ImageID)
		fs.cacheMgr.SchedGC()
	}
	return nil
}

func (fs *filesystem) Cleanup(ctx context.Context) error {
	if !fs.hasDaemon() {
		return nil
	}

	for _, d := range fs.manager.ListDaemons() {
		err := fs.Umount(ctx, filepath.Dir(d.MountPoint()))
		if err != nil {
			log.G(ctx).Infof("failed to umount %s err %+v", d.MountPoint(), err)
		}
	}
	return nil
}

func (fs *filesystem) MountPoint(snapshotID string) (string, error) {
	if !fs.hasDaemon() {
		// For NoneDaemon mode, just return error to use snapshotter
		// default mount point path
		return "", fmt.Errorf("don't need nydus daemon of snapshot %s", snapshotID)
	}

	if d, err := fs.manager.GetBySnapshotID(snapshotID); err == nil {
		if fs.mode == fspkg.SharedInstance {
			return d.SharedMountPoint(), nil
		}
		return d.MountPoint(), nil
	}
	return "", fmt.Errorf("failed to find nydus mountpoint of snapshot %s", snapshotID)
}

func (fs *filesystem) BootstrapFile(id string) (string, error) {
	return daemon.GetBootstrapFile(fs.SnapshotRoot(), id)
}

func (fs *filesystem) NewDaemonConfig(labels map[string]string) (config.DaemonConfig, error) {
	imageID, ok := labels[label.ImageRef]
	if !ok {
		return config.DaemonConfig{}, fmt.Errorf("no image ID found in label")
	}

	cfg, err := config.NewDaemonConfig(fs.daemonCfg, imageID, fs.vpcRegistry, labels)
	if err != nil {
		return config.DaemonConfig{}, err
	}

	if fs.cacheMgr != nil {
		// Overriding work_dir option of nyudsd config as we want to set it
		// via snapshotter config option to let snapshotter handle blob cache GC.
		cfg.Device.Cache.Config.WorkDir = fs.cacheMgr.CacheDir()
	}
	return cfg, nil
}

func (fs *filesystem) mount(d *daemon.Daemon, labels map[string]string) error {
	err := fs.generateDaemonConfig(d, labels)
	if err != nil {
		return err
	}
	if fs.mode == fspkg.SharedInstance || fs.mode == fspkg.PrefetchInstance {
		err = d.SharedMount()
		if err != nil {
			return errors.Wrapf(err, "failed to shared mount")
		}
		return fs.addSnapshot(d.ImageID, labels)
	}
	if err := fs.manager.StartDaemon(d); err != nil {
		return errors.Wrapf(err, "start daemon err")
	}
	return fs.addSnapshot(d.ImageID, labels)
}

func (fs *filesystem) addSnapshot(imageID string, labels map[string]string) error {
	// Do nothing if there's no cacheMgr
	if fs.cacheMgr == nil {
		return nil
	}

	blobs, err := fs.getBlobIDs(labels)
	if err != nil {
		return err
	}
	log.L.Infof("image %s with blob caches %v", imageID, blobs)
	return fs.cacheMgr.AddSnapshot(imageID, blobs)
}

func (fs *filesystem) initSharedDaemon(ctx context.Context) (_ *daemon.Daemon, retErr error) {
	d, err := fs.newSharedDaemon()
	if err != nil {
		return nil, errors.Wrap(err, "failed to init shared daemon")
	}

	defer func() {
		if retErr != nil {
			fs.manager.DeleteDaemon(d)
		}
	}()
	if err := fs.manager.StartDaemon(d); err != nil {
		return nil, errors.Wrap(err, "failed to start shared daemon")
	}
	fs.sharedDaemon = d

	return d, nil
}

func (fs *filesystem) newDaemon(ctx context.Context, snapshotID string, imageID string) (_ *daemon.Daemon, retErr error) {
	if fs.mode == fspkg.SharedInstance || fs.mode == fspkg.PrefetchInstance {
		// Check if daemon is already running
		d, err := fs.getSharedDaemon()
		if err == nil && d != nil {
			if fs.sharedDaemon == nil {
				fs.sharedDaemon = d
				log.G(ctx).Infof("daemon(ID=%s) is already running and reconnected", daemon.SharedNydusDaemonID)
			}
		} else {
			_, err = fs.initSharedDaemon(ctx)
			if err != nil {
				// AlreadyExists means someone else has initialized shared daemon.
				if !errdefs.IsAlreadyExists(err) {
					return nil, err
				}
			}

			// We don't need to wait instance to be ready in PrefetchInstance mode, as we want
			// to return snapshot to containerd as soon as possible, and prefetch instance is
			// only for prefetch.
			if fs.mode != fspkg.PrefetchInstance {
				if err := fs.WaitUntilReady(ctx, daemon.SharedNydusDaemonID); err != nil {
					return nil, errors.Wrap(err, "failed to wait shared daemon")
				}
			}
		}
		return fs.createSharedDaemon(snapshotID, imageID)
	}
	return fs.createNewDaemon(snapshotID, imageID)
}

// Find saved sharedDaemon first, if not found then find it in db
func (fs *filesystem) getSharedDaemon() (*daemon.Daemon, error) {
	if fs.sharedDaemon != nil {
		return fs.sharedDaemon, nil
	}

	d, err := fs.manager.GetByID(daemon.SharedNydusDaemonID)
	return d, err
}

// createNewDaemon create new nydus daemon by snapshotID and imageID
func (fs *filesystem) createNewDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
	var (
		d   *daemon.Daemon
		err error
	)
	d, _ = fs.manager.GetBySnapshotID(snapshotID)
	if d != nil {
		return nil, errdefs.ErrAlreadyExists
	}
	customMountPoint := filepath.Join(fs.SnapshotRoot(), snapshotID, "mnt")
	if d, err = daemon.NewDaemon(
		daemon.WithSnapshotID(snapshotID),
		daemon.WithSocketDir(fs.SocketRoot()),
		daemon.WithConfigDir(fs.ConfigRoot()),
		daemon.WithSnapshotDir(fs.SnapshotRoot()),
		daemon.WithLogDir(fs.logDir),
		daemon.WithImageID(imageID),
		daemon.WithLogLevel(fs.logLevel),
		daemon.WithLogToStdout(fs.logToStdout),
		daemon.WithCustomMountPoint(customMountPoint),
		daemon.WithNydusdThreadNum(fs.nydusdThreadNum),
	); err != nil {
		return nil, err
	}
	if err = fs.manager.NewDaemon(d); err != nil {
		return nil, err
	}
	return d, nil
}

// createSharedDaemon create an virtual daemon from global shared daemon instance
// the global shared daemon with an special ID "shared_daemon", all virtual daemons are
// created from this daemon with api invocation
func (fs *filesystem) createSharedDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
	var (
		sharedDaemon *daemon.Daemon
		d            *daemon.Daemon
		err          error
	)
	d, _ = fs.manager.GetBySnapshotID(snapshotID)
	if d != nil {
		return nil, errdefs.ErrAlreadyExists
	}
	sharedDaemon, err = fs.getSharedDaemon()
	if err != nil {
		return nil, err
	}
	if d, err = daemon.NewDaemon(
		daemon.WithSnapshotID(snapshotID),
		daemon.WithRootMountPoint(*sharedDaemon.RootMountPoint),
		daemon.WithSnapshotDir(fs.SnapshotRoot()),
		daemon.WithAPISock(sharedDaemon.GetAPISock()),
		daemon.WithConfigDir(fs.ConfigRoot()),
		daemon.WithLogDir(fs.logDir),
		daemon.WithImageID(imageID),
		daemon.WithLogLevel(fs.logLevel),
		daemon.WithLogToStdout(fs.logToStdout),
		daemon.WithNydusdThreadNum(fs.nydusdThreadNum),
	); err != nil {
		return nil, err
	}
	if err = fs.manager.NewDaemon(d); err != nil {
		return nil, err
	}
	return d, nil
}

// generateDaemonConfig generate Daemon configuration
func (fs *filesystem) generateDaemonConfig(d *daemon.Daemon, labels map[string]string) error {
	cfg, err := config.NewDaemonConfig(fs.daemonCfg, d.ImageID, fs.vpcRegistry, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to generate daemon config for daemon %s", d.ID)
	}

	if fs.cacheMgr != nil {
		// Overriding work_dir option of nyudsd config as we want to set it
		// via snapshotter config option to let snapshotter handle blob cache GC.
		cfg.Device.Cache.Config.WorkDir = fs.cacheMgr.CacheDir()
	}
	return config.SaveConfig(cfg, d.ConfigFile())
}

func (fs *filesystem) hasDaemon() bool {
	return fs.mode != fspkg.NoneInstance && fs.mode != fspkg.PrefetchInstance
}

func (fs *filesystem) getBlobIDs(labels map[string]string) ([]string, error) {
	idStr, ok := labels[LayerAnnotationNydusBlobIDs]
	if !ok {
		return nil, errors.New("no blob ids found")
	}
	var result []string
	if err := json.Unmarshal([]byte(idStr), &result); err != nil {
		return nil, err
	}
	return result, nil
}
