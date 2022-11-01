/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

// Abstraction layer of underlying file systems. The file system could be mounted by one
// or more nydusd daemons. fs package hides the details

package fs

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/KarpelesLab/reflink"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/mohae/deepcopy"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/daemon/types"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/manager"
	"github.com/containerd/nydus-snapshotter/pkg/signature"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

// RafsV6 layout: 1k + SuperBlock(128) + SuperBlockExtener(256)
// RafsV5 layout: 8K superblock
// So we only need to read the MaxSuperBlockSize size to include both v5 and v6 superblocks
const MaxSuperBlockSize = 8 * 1024
const (
	RafsV5                 string = "v5"
	RafsV6                 string = "v6"
	RafsV5SuperVersion     uint32 = 0x500
	RafsV5SuperMagic       uint32 = 0x5241_4653
	RafsV6SuperMagic       uint32 = 0xE0F5_E1E2
	RafsV6SuperBlockSize   uint32 = 1024 + 128 + 256
	RafsV6SuperBlockOffset uint32 = 1024
	RafsV6ChunkInfoOffset  uint32 = 1024 + 128 + 24
	BootstrapFile          string = "image/image.boot"
	LegacyBootstrapFile    string = "image.boot"
	DummyMountpoint        string = "/dummy"
)

var nativeEndian binary.ByteOrder

type ImageMode int

const (
	OnDemand ImageMode = iota
	PreLoad
)

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

type Filesystem struct {
	meta.FileSystemMeta
	// Managing all daemons serving filesystem.
	Manager              *manager.Manager
	cacheMgr             *cache.Manager
	sharedDaemon         *daemon.Daemon
	stargzResolver       *stargz.Resolver
	verifier             *signature.Verifier
	fsDriver             string
	nydusdBinaryPath     string
	nydusImageBinaryPath string
	nydusdThreadNum      int
	logLevel             string
	logDir               string
	logToStdout          bool
	vpcRegistry          bool
	mode                 config.DaemonMode
}

// NewFileSystem initialize Filesystem instance
// TODO(chge): `Filesystem` abstraction is not suggestive. A snapshotter
// can mount many Rafs/Erofs file systems
func NewFileSystem(ctx context.Context, opt ...NewFSOpt) (*Filesystem, error) {
	var fs Filesystem
	for _, o := range opt {
		err := o(&fs)
		if err != nil {
			return nil, err
		}
	}

	var recoveringDaemons []*daemon.Daemon
	var err error

	// Try to reconnect to running daemons
	if recoveringDaemons, err = fs.Manager.Reconnect(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to reconnect daemons")
	}

	var sharedDaemonConnected = false

	// Try to bring up the shared daemon early.
	if fs.mode == config.DaemonModeShared {
		// Situations that shared daemon is not found:
		//   1. The first time this nydus-snapshotter runs
		//   2. Daemon record is wrongly deleted from DB. Above reconnecting already gathers
		//		all daemons but still not found shared daemon. The best workaround is to start
		//		a new nydusd for it.
		// TODO: We still need to consider shared daemon the time sequence of initializing daemon,
		// start daemon commit its state to DB and retrieving its state.
		if d := fs.getSharedDaemon(); (d != nil && !d.Connected) || d == nil {
			log.G(ctx).Infof("initializing the shared nydus daemon")
			if err := fs.initSharedDaemon(); err != nil {
				return nil, errors.Wrap(err, "start shared nydusd daemon")
			}

			sharedDaemonConnected = true
		}
	}

	// Try to bring all persisted and stopped nydusds up and remount Rafs
	if len(recoveringDaemons) != 0 {
		for _, d := range recoveringDaemons {
			if fs.mode == config.DaemonModeShared {
				if d.ID != daemon.SharedNydusDaemonID && sharedDaemonConnected {
					// FIXME: Fix the trick
					d.Supervisor = fs.Manager.SupervisorSet.GetSupervisor("shared_daemon")
					if err := d.SharedMount(); err != nil {
						return nil, errors.Wrap(err, "mount rafs when recovering")
					}
				}
			} else {
				if !d.Connected {
					if err := fs.Manager.StartDaemon(d); err != nil {
						return nil, errors.Wrapf(err, "start nydusd %s", d.ID)
					}
				}
			}
		}
	}

	return &fs, nil
}

func (fs *Filesystem) createSharedDaemon() (*daemon.Daemon, error) {

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
		daemon.WithFsDriver(fs.fsDriver),
	)
	if err != nil {
		return nil, err
	}

	if err := fs.Manager.NewDaemon(d); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return nil, err
		}
	}

	// Supervisor is strongly associated with real running nydusd daemon.
	su := fs.Manager.SupervisorSet.NewSupervisor(d.ID)
	if su == nil {
		return nil, errors.Errorf("fail to create supervisor for daemon %s", d.ID)

	}
	d.Supervisor = su

	return d, nil
}

func (fs *Filesystem) StargzEnabled() bool {
	return fs.stargzResolver != nil
}

func (fs *Filesystem) IsStargzDataLayer(ctx context.Context, labels map[string]string) (bool, string, string, *stargz.Blob) {
	if !fs.StargzEnabled() {
		return false, "", "", nil
	}
	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return false, "", "", nil
	}

	log.G(ctx).Infof("image ref %s digest %s", ref, layerDigest)
	keychain, err := auth.GetKeyChainByRef(ref, labels)
	if err != nil {
		logrus.WithError(err).Warn("get key chain by ref")
		return false, ref, layerDigest, nil
	}
	blob, err := fs.stargzResolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		logrus.WithError(err).Warn("get stargz blob")
		return false, ref, layerDigest, nil
	}
	off, err := blob.GetTocOffset()
	if err != nil {
		logrus.WithError(err).Warn("get toc offset")
		return false, ref, layerDigest, nil
	}
	if off <= 0 {
		logrus.WithError(err).Warnf("invalid stargz toc offset %d", off)
		return false, ref, layerDigest, nil
	}

	return true, ref, layerDigest, blob
}

func (fs *Filesystem) MergeStargzMetaLayer(ctx context.Context, s storage.Snapshot) error {
	mergedDir := fs.UpperPath(s.ParentIDs[0])
	mergedBootstrap := filepath.Join(mergedDir, "image.boot")
	if _, err := os.Stat(mergedBootstrap); err == nil {
		return nil
	}

	bootstraps := []string{}
	for idx, snapshotID := range s.ParentIDs {
		files, err := os.ReadDir(fs.UpperPath(snapshotID))
		if err != nil {
			return errors.Wrap(err, "read snapshot dir")
		}

		bootstrapName := ""
		blobMetaName := ""
		for _, file := range files {
			if digest.Digest(fmt.Sprintf("sha256:%s", file.Name())).Validate() == nil {
				bootstrapName = file.Name()
			}
			if strings.HasSuffix(file.Name(), "blob.meta") {
				blobMetaName = file.Name()
			}
		}
		if bootstrapName == "" {
			return fmt.Errorf("can't find bootstrap for snapshot %s", snapshotID)
		}

		// The blob meta file is generated in corresponding snapshot dir for each layer,
		// but we need copy them to fscache work dir for nydusd use. This is not an
		// efficient method, but currently nydusd only supports reading blob meta files
		// from the same dir, so it is a workaround. If performance is a concern, it is
		// best to convert the estargz image TOC file to a bootstrap / blob meta file
		// at build time.
		if blobMetaName != "" && idx != 0 {
			sourcePath := filepath.Join(fs.UpperPath(snapshotID), blobMetaName)
			// This path is same with `d.FscacheWorkDir()`, it's for fscache work dir.
			targetPath := filepath.Join(fs.UpperPath(s.ParentIDs[0]), blobMetaName)
			if err := reflink.Auto(sourcePath, targetPath); err != nil {
				return errors.Wrap(err, "copy source blob.meta to target")
			}
		}

		bootstrapPath := filepath.Join(fs.UpperPath(snapshotID), bootstrapName)
		bootstraps = append([]string{bootstrapPath}, bootstraps...)
	}

	if len(bootstraps) == 1 {
		if err := reflink.Auto(bootstraps[0], mergedBootstrap); err != nil {
			return errors.Wrap(err, "copy source meta blob to target")
		}
	} else {
		tf, err := os.CreateTemp(mergedDir, "merging-stargz")
		if err != nil {
			return errors.Wrap(err, "create temp file for merging stargz layers")
		}
		defer func() {
			if err != nil {
				os.Remove(tf.Name())
			}
			tf.Close()
		}()

		options := []string{
			"merge",
			"--bootstrap", tf.Name(),
		}
		options = append(options, bootstraps...)
		cmd := exec.Command(fs.nydusImageBinaryPath, options...)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		log.G(ctx).Infof("nydus image command %v", options)
		err = cmd.Run()
		if err != nil {
			return errors.Wrap(err, "merging stargz layers")
		}

		err = os.Rename(tf.Name(), mergedBootstrap)
		if err != nil {
			return errors.Wrap(err, "rename merged stargz layers")
		}
		os.Chmod(mergedBootstrap, 0440)
	}

	return nil
}

func (fs *Filesystem) PrepareStargzMetaLayer(ctx context.Context, blob *stargz.Blob, ref, layerDigest string, s storage.Snapshot, labels map[string]string) error {
	if !fs.StargzEnabled() {
		return fmt.Errorf("stargz is not enabled")
	}

	upperPath := fs.UpperPath(s.ID)
	blobID := digest.Digest(layerDigest).Hex()
	convertedBootstrap := filepath.Join(upperPath, blobID)
	stargzFile := filepath.Join(upperPath, stargz.TocFileName)
	if _, err := os.Stat(convertedBootstrap); err == nil {
		return nil
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.G(ctx).Infof("total stargz prepare layer duration %d", duration.Milliseconds())
	}()

	r, err := blob.ReadToc()
	if err != nil {
		return errors.Wrapf(err, "failed to read toc from ref %s, digest %s", ref, layerDigest)
	}
	starGzToc, err := os.OpenFile(stargzFile, os.O_CREATE|os.O_RDWR, 0640)
	if err != nil {
		return errors.Wrap(err, "failed to create stargz index")
	}

	defer starGzToc.Close()

	_, err = io.Copy(starGzToc, r)
	if err != nil {
		return errors.Wrap(err, "failed to save stargz index")
	}
	os.Chmod(stargzFile, 0440)

	blobMetaPath := filepath.Join(fs.cacheMgr.CacheDir(), fmt.Sprintf("%s.blob.meta", blobID))
	if fs.fsDriver == config.FsDriverFscache {
		// For fscache, the cache directory is managed linux fscache driver, so the blob.meta file
		// can't be stored there.
		if err := os.MkdirAll(upperPath, 0750); err != nil {
			return errors.Wrapf(err, "failed to create fscache work dir %s", upperPath)
		}
		blobMetaPath = filepath.Join(upperPath, fmt.Sprintf("%s.blob.meta", blobID))
	}

	tf, err := os.CreateTemp(upperPath, "converting-stargz")
	if err != nil {
		return errors.Wrap(err, "create temp file for merging stargz layers")
	}
	defer func() {
		if err != nil {
			os.Remove(tf.Name())
		}
		tf.Close()
	}()

	options := []string{
		"create",
		"--source-type", "stargz_index",
		"--bootstrap", tf.Name(),
		"--blob-id", blobID,
		"--repeatable",
		"--disable-check",
		// FIXME: allow user to specify fs version and automatically detect
		// chunk size and compressor from estargz TOC file.
		"--fs-version", "6",
		"--chunk-size", "0x400000",
		"--blob-meta", blobMetaPath,
	}
	options = append(options, filepath.Join(fs.UpperPath(s.ID), stargz.TocFileName))
	cmd := exec.Command(fs.nydusImageBinaryPath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.G(ctx).Infof("nydus image command %v", options)
	err = cmd.Run()
	if err != nil {
		return errors.Wrap(err, "converting stargz layer")
	}

	err = os.Rename(tf.Name(), convertedBootstrap)
	if err != nil {
		return errors.Wrap(err, "rename converted stargz layer")
	}
	os.Chmod(convertedBootstrap, 0440)

	return nil
}

func (fs *Filesystem) StargzLayer(labels map[string]string) bool {
	return labels[label.StargzLayer] != ""
}

func (fs *Filesystem) IsNydusDataLayer(ctx context.Context, labels map[string]string) bool {
	_, dataOk := labels[label.NydusDataLayer]
	return dataOk
}

func (fs *Filesystem) IsNydusMetaLayer(ctx context.Context, labels map[string]string) bool {
	_, dataOk := labels[label.NydusMetaLayer]
	return dataOk
}

// Mount will be called when containerd snapshotter prepare remote snapshotter
// this method will fork nydus daemon and manage it in the internal store, and indexed by snapshotID
func (fs *Filesystem) Mount(snapshotID string, labels map[string]string) (err error) {
	// If NoneDaemon mode, we don't mount nydus on host
	if !fs.hasDaemon() {
		return nil
	}

	imageID, ok := labels[label.CRIImageRef]
	if !ok {
		return fmt.Errorf("failed to find image ref of snapshot %s, labels %v", snapshotID, labels)
	}

	cfg := deepcopy.Copy(fs.Manager.DaemonConfig).(daemonconfig.DaemonConfig)

	d, err := fs.newDaemon(snapshotID, imageID)
	// if daemon already exists for snapshotID, just return
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	defer func() {
		if err != nil {
			_ = fs.Manager.DestroyDaemon(d)
		}
	}()

	bootstrap, err := d.BootstrapFile()
	if err != nil {
		return errors.Wrapf(err, "find bootstrap file of daemon %s", d.ID)
	}
	workDir := d.FscacheWorkDir()
	// Nydusd uses cache manager's directory to store blob caches. So cache
	// manager knows where to find those blobs.
	cacheDir := fs.cacheMgr.CacheDir()

	params := map[string]string{
		daemonconfig.Bootstrap: bootstrap,
		// FIXME: Does nydusd really stores cache files here?
		daemonconfig.WorkDir:  workDir,
		daemonconfig.CacheDir: cacheDir}

	daemonconfig.SupplementDaemonConfig(cfg, imageID, d.SnapshotID, false, labels, params)

	// Associate daemon config object when creating a new daemon object.
	// Avoid reading disk file again and again.
	// The daemon could be a virtual daemon representing a rafs mount
	d.Config = cfg

	// TODO: How to manage rafs configurations ondisk? separated json config file or DB record?
	// In order to recover erofs mount, the configuration file has to be persisted.
	err = d.Config.DumpFile(d.ConfigFile())
	if errors.Is(err, errdefs.ErrAlreadyExists) {
		log.L.Debugf("Configuration file %s already exits", d.ConfigFile())
	}

	// if publicKey is not empty we should verify bootstrap file of image
	err = fs.verifier.Verify(labels, bootstrap)
	if err != nil {
		return errors.Wrapf(err, "verify signature of daemon %s", d.ID)
	}

	err = fs.mount(d)
	if err != nil {
		log.L.Errorf("failed to mount %s, %s", d.MountPoint(), err)
		return errors.Wrapf(err, "mount file system by daemon %s", d.ID)
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

// WaitUntilReady wait until daemon ready by snapshotID, it will wait until nydus domain socket established
// and the status of nydusd daemon must be ready
func (fs *Filesystem) WaitUntilReady(snapshotID string) error {
	// If NoneDaemon mode, there's no need to wait for daemon ready
	if !fs.hasDaemon() {
		return nil
	}

	d := fs.Manager.GetBySnapshotID(snapshotID)
	if d == nil {
		return errdefs.ErrNotFound
	}

	return d.WaitUntilState(types.DaemonStateRunning)
}

func (fs *Filesystem) Umount(ctx context.Context, mountPoint string) error {
	if !fs.hasDaemon() {
		return nil
	}

	id := filepath.Base(mountPoint)
	daemon := fs.Manager.GetBySnapshotID(id)

	if daemon == nil {
		log.L.Infof("snapshot %s does not correspond to a nydusd", id)
		return nil
	}

	log.L.Infof("umount snapshot %s, daemon ID %s", id, daemon.ID)

	if err := fs.Manager.DestroyDaemon(daemon); err != nil {
		return errors.Wrap(err, "destroy daemon err")
	}

	return nil
}

func (fs *Filesystem) Cleanup(ctx context.Context) error {
	if !fs.hasDaemon() {
		return nil
	}

	for _, d := range fs.Manager.ListDaemons() {
		err := fs.Umount(ctx, filepath.Dir(d.MountPoint()))
		if err != nil {
			log.G(ctx).Infof("failed to umount %s err %+v", d.MountPoint(), err)
		}
	}
	return nil
}

func (fs *Filesystem) MountPoint(snapshotID string) (string, error) {
	if !fs.hasDaemon() {
		// For NoneDaemon mode, return a dummy mountpoint which is very likely not
		// existed on host. NoneDaemon mode does not start nydusd, so NO fuse mount is
		// ever performed. Only mount option carries meaningful info to containerd and
		// finally passes to shim.
		return DummyMountpoint, nil
	}

	if d := fs.Manager.GetBySnapshotID(snapshotID); d != nil {
		// Working for fscache driver, only one nydusd with multiple mountpoints.
		// So it is not ordinary SharedMode.
		if d.FsDriver == config.FsDriverFscache {
			return d.MountPoint(), nil
		}

		if fs.mode == config.DaemonModeShared {
			return d.SharedAbsMountPoint(), nil
		}

		return d.MountPoint(), nil
	}

	return "", fmt.Errorf("failed to find nydus mountpoint of snapshot %s", snapshotID)
}

func (fs *Filesystem) BootstrapFile(id string) (string, error) {
	return daemon.GetBootstrapFile(fs.SnapshotRoot(), id)
}

func (fs *Filesystem) mount(d *daemon.Daemon) error {
	d.Config.DumpFile(d.ConfigDir)

	if fs.mode == config.DaemonModeShared {
		if err := d.SharedMount(); err != nil {
			return errors.Wrapf(err, "failed to shared mount")
		}
	} else if err := fs.Manager.StartDaemon(d); err != nil {
		return errors.Wrapf(err, "start daemon")
	}

	return nil
}

// 1. Create a daemon instance
// 2. Build command line
// 3. Start daemon
func (fs *Filesystem) initSharedDaemon() (err error) {
	d, err := fs.createSharedDaemon()
	if err != nil {
		return errors.Wrap(err, "failed to initialize shared daemon")
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
	d.Config = fs.Manager.DaemonConfig
	d.Config.DumpFile(d.ConfigFile())

	if err := fs.Manager.StartDaemon(d); err != nil {
		return errors.Wrap(err, "start shared daemon")
	}

	fs.sharedDaemon = d

	return
}

func (fs *Filesystem) newDaemon(snapshotID string, imageID string) (_ *daemon.Daemon, retErr error) {
	if fs.mode == config.DaemonModeShared {
		// Check if daemon is already running
		d := fs.getSharedDaemon()
		if d != nil {
			if fs.sharedDaemon == nil {
				fs.sharedDaemon = d
				log.L.Infof("daemon(ID=%s) is already running and reconnected", daemon.SharedNydusDaemonID)
			}
		} else {
			err := fs.initSharedDaemon()
			if err != nil {
				// AlreadyExists means someone else has initialized shared daemon.
				if !errdefs.IsAlreadyExists(err) {
					return nil, err
				}
			}

			// We don't need to wait instance to be ready in PrefetchInstance mode, as we want
			// to return snapshot to containerd as soon as possible, and prefetch instance is
			// only for prefetch.
			if err := fs.WaitUntilReady(daemon.SharedNydusDaemonID); err != nil {
				return nil, errors.Wrap(err, "failed to wait shared daemon")
			}
		}
		return fs.createVirtualDaemon(snapshotID, imageID)
	}
	return fs.createDaemon(snapshotID, imageID)
}

// Find saved sharedDaemon first, if not found then find it in db
func (fs *Filesystem) getSharedDaemon() *daemon.Daemon {

	if fs.sharedDaemon != nil {
		return fs.sharedDaemon
	}

	d := fs.Manager.GetByDaemonID(daemon.SharedNydusDaemonID)
	return d
}

// createDaemon create new nydus daemon by snapshotID and imageID
func (fs *Filesystem) createDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
	var (
		d   *daemon.Daemon
		err error
	)

	d = fs.Manager.GetBySnapshotID(snapshotID)
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
		daemon.WithFsDriver(fs.fsDriver),
	); err != nil {
		return nil, err
	}

	if err = fs.Manager.NewDaemon(d); err != nil {
		return nil, err
	}

	// Supervisor is strongly associated with real running nydusd daemon.
	su := fs.Manager.SupervisorSet.NewSupervisor(d.ID)
	if su == nil {
		return nil, errors.Errorf("fail to create supervisor for daemon %s", d.ID)

	}
	d.Supervisor = su

	return d, nil
}

// Create a virtual daemon as placeholder in DB and daemon states cache to represent a Rafs mount.
// It does not fork any new nydusd process. The rafs umount is always done by requesting to the running
// nydusd API server.
func (fs *Filesystem) createVirtualDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
	var (
		sharedDaemon *daemon.Daemon
		d            *daemon.Daemon
		err          error
	)

	d = fs.Manager.GetBySnapshotID(snapshotID)
	if d != nil {
		return nil, errdefs.ErrAlreadyExists
	}

	sharedDaemon = fs.getSharedDaemon()
	if sharedDaemon == nil {
		return nil, errdefs.ErrNotFound
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
		daemon.WithFsDriver(fs.fsDriver),
	); err != nil {
		return nil, err
	}

	if err = fs.Manager.NewDaemon(d); err != nil {
		return nil, err
	}

	// FIXME: It's a little tricky
	d.Supervisor = fs.Manager.SupervisorSet.GetSupervisor("shared_daemon")

	return d, nil
}

func (fs *Filesystem) hasDaemon() bool {
	return fs.mode != config.DaemonModeNone
}

func isRafsV6(buf []byte) bool {
	return nativeEndian.Uint32(buf[RafsV6SuperBlockOffset:]) == RafsV6SuperMagic
}

func DetectFsVersion(header []byte) (string, error) {
	if len(header) < 8 {
		return "", errors.New("header buffer to DetectFsVersion is too small")
	}
	magic := binary.LittleEndian.Uint32(header[0:4])
	fsVersion := binary.LittleEndian.Uint32(header[4:8])
	if magic == RafsV5SuperMagic && fsVersion == RafsV5SuperVersion {
		return RafsV5, nil
	}

	// FIXME: detect more magic numbers to reduce collision
	if len(header) >= int(RafsV6SuperBlockSize) && isRafsV6(header) {
		return RafsV6, nil
	}

	return "", errors.New("unknown file system header")
}
