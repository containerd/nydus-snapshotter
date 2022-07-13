/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/cache"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/fs/stargz"
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
const RafsV6ChunkInfoOffset = 1024 + 128 + 24
const RafsV6SuppeOffset = 1024
const BootstrapFile = "image/image.boot"
const LegacyBootstrapFile = "image.boot"

var nativeEndian binary.ByteOrder

type Mode int

const (
	SharedInstance Mode = iota
	MultiInstance
	NoneInstance
	PrefetchInstance
)

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
	manager              *process.Manager
	cacheMgr             *cache.Manager
	verifier             *signature.Verifier
	sharedDaemon         *daemon.Daemon
	daemonCfg            config.DaemonConfig
	resolver             *Resolver
	stargzResolver       *stargz.Resolver
	vpcRegistry          bool
	fsDriver             string
	nydusdBinaryPath     string
	nydusImageBinaryPath string
	mode                 Mode
	logLevel             string
	logDir               string
	logToStdout          bool
	nydusdThreadNum      int
	imageMode            ImageMode
	blobMgr              *BlobManager
}

func getBlobPath(dir string, blobDigest string) (string, error) {
	digest, err := digest.Parse(blobDigest)
	if err != nil {
		return "", errors.Wrapf(err, "invalid layer digest %s", blobDigest)
	}
	return filepath.Join(dir, digest.Encoded()), nil
}

// NewFileSystem initialize Filesystem instance
func NewFileSystem(ctx context.Context, opt ...NewFSOpt) (*Filesystem, error) {
	var fs Filesystem
	for _, o := range opt {
		err := o(&fs)
		if err != nil {
			return nil, err
		}
	}
	if fs.imageMode == PreLoad {
		fs.blobMgr = NewBlobManager(fs.daemonCfg.Device.Backend.Config.Dir)
		go func() {
			err := fs.blobMgr.Run(ctx)
			if err != nil {
				log.G(ctx).Warnf("blob manager run failed %s", err)
			}
		}()
	}
	fs.resolver = NewResolver()
	// Try to reconnect to running daemons
	if err := fs.manager.Reconnect(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to reconnect daemons")
	}
	return &fs, nil
}

func (fs *Filesystem) CleanupBlobLayer(ctx context.Context, key string, async bool) error {
	if fs.blobMgr == nil {
		return nil
	}
	return fs.blobMgr.Remove(key, async)
}

func (fs *Filesystem) PrepareBlobLayer(ctx context.Context, snapshot storage.Snapshot, labels map[string]string) error {
	if fs.imageMode != PreLoad {
		return nil
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.G(ctx).Infof("total nydus prepare data layer duration %d ms", duration.Milliseconds())
	}()

	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return fmt.Errorf("can not find ref and digest from label %+v", labels)
	}
	blobPath, err := getBlobPath(fs.blobMgr.GetBlobDir(), layerDigest)
	if err != nil {
		return errors.Wrap(err, "failed to get blob path")
	}
	_, err = os.Stat(blobPath)
	if err == nil {
		log.G(ctx).Debugf("%s blob layer already exists", blobPath)
		return nil
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, "Unexpected error, we can't handle it")
	}

	readerCloser, err := fs.resolver.Resolve(ref, layerDigest, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve from ref %s, digest %s", ref, layerDigest)
	}
	defer readerCloser.Close()

	blobFile, err := os.OpenFile(blobPath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return errors.Wrap(err, "failed to create blob file")
	}
	defer blobFile.Close()

	_, err = io.Copy(blobFile, readerCloser)
	if err != nil {
		if err := os.Remove(blobPath); err != nil {
			log.G(ctx).Warnf("failed to remove blob file %s", blobPath)
		}
		return errors.Wrap(err, "write blob to local file")
	}

	return err
}

func (fs *Filesystem) newSharedDaemon() (*daemon.Daemon, error) {
	modeOpt := daemon.WithSharedDaemon()
	if fs.mode == PrefetchInstance {
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
		daemon.WithFsDriver(fs.fsDriver),
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

func (fs *Filesystem) SupportStargz(ctx context.Context, labels map[string]string) bool {
	if fs.stargzResolver == nil {
		return false
	}
	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return false
	}
	log.G(ctx).Infof("image ref %s digest %s", ref, layerDigest)
	keychain, err := auth.GetKeyChainByRef(ref, labels)
	if err != nil {
		logrus.WithError(err).Warn("get key chain by ref")
		return false
	}
	blob, err := fs.stargzResolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		logrus.WithError(err).Warn("get stargz blob")
		return false
	}
	off, err := blob.GetTocOffset()
	if err != nil {
		logrus.WithError(err).Warn("get toc offset")
		return false
	}
	if off <= 0 {
		logrus.WithError(err).Warnf("invalid stargz toc offset %d", off)
		return false
	}
	return true
}

func (fs *Filesystem) MergeStargzMetaLayer(ctx context.Context, s storage.Snapshot) error {
	mergedBootstrap := filepath.Join(fs.UpperPath(s.ParentIDs[0]), "image.boot")
	if _, err := os.Stat(mergedBootstrap); err == nil {
		return nil
	}

	bootstraps := []string{}
	for idx, snapshotID := range s.ParentIDs {
		files, err := ioutil.ReadDir(fs.UpperPath(snapshotID))
		if err != nil {
			if err != nil {
				return errors.Wrap(err, "read snapshot dir")
			}
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
			sourceFile, err := os.Open(sourcePath)
			if err != nil {
				return errors.Wrapf(err, "open source blob meta file %s", sourcePath)
			}
			defer sourceFile.Close()

			// This path is same with `d.FscacheWorkDir()`, it's for fscache work dir.
			targetPath := filepath.Join(fs.UpperPath(s.ParentIDs[0]), blobMetaName)
			targetFile, err := os.Create(targetPath)
			if err != nil {
				return errors.Wrapf(err, "create target blob meta file %s", targetPath)
			}
			defer targetFile.Close()

			if _, err := io.Copy(targetFile, sourceFile); err != nil {
				return errors.Wrap(err, "copy source blob meta to target")
			}
		}

		bootstrapPath := filepath.Join(fs.UpperPath(snapshotID), bootstrapName)
		bootstraps = append([]string{bootstrapPath}, bootstraps...)
	}

	options := []string{
		"merge",
		"--bootstrap", filepath.Join(fs.UpperPath(s.ParentIDs[0]), "image.boot"),
	}
	options = append(options, bootstraps...)
	log.G(ctx).Infof("nydus image command %v", options)

	cmd := exec.Command(fs.nydusImageBinaryPath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	return cmd.Run()
}

func (fs *Filesystem) PrepareStargzMetaLayer(ctx context.Context, s storage.Snapshot, labels map[string]string) error {
	if fs.stargzResolver == nil {
		return fmt.Errorf("stargz is not enabled")
	}
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.G(ctx).Infof("total stargz prepare layer duration %d", duration.Milliseconds())
	}()
	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return fmt.Errorf("can not find ref and digest from label %+v", labels)
	}
	keychain, err := auth.GetKeyChainByRef(ref, labels)
	if err != nil {
		return errors.Wrap(err, "get key chain")
	}
	blob, err := fs.stargzResolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		return errors.Wrapf(err, "failed to get blob from ref %s, digest %s", ref, layerDigest)
	}
	r, err := blob.ReadToc()
	if err != nil {
		return errors.Wrapf(err, "failed to read toc from ref %s, digest %s", ref, layerDigest)
	}
	starGzToc, err := os.OpenFile(filepath.Join(fs.UpperPath(s.ID), stargz.TocFileName), os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create stargz index")
	}
	_, err = io.Copy(starGzToc, r)
	if err != nil {
		return errors.Wrap(err, "failed to save stargz index")
	}
	blobID := digest.Digest(layerDigest).Hex()
	blobMetaPath := filepath.Join(fs.cacheMgr.CacheDir(), fmt.Sprintf("%s.blob.meta", blobID))

	if fs.fsDriver == config.FsDriverFscache {
		blobMetaDir := fs.UpperPath(s.ID)
		if err := os.MkdirAll(blobMetaDir, 0755); err != nil {
			return errors.Wrapf(err, "failed to create fscache work dir %s", blobMetaDir)
		}
		blobMetaPath = filepath.Join(blobMetaDir, fmt.Sprintf("%s.blob.meta", blobID))
	}

	options := []string{
		"create",
		"--source-type", "stargz_index",
		"--bootstrap", filepath.Join(fs.UpperPath(s.ID), blobID),
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
	log.G(ctx).Infof("nydus image command %v", options)

	cmd := exec.Command(fs.nydusImageBinaryPath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	return cmd.Run()
}

func (fs *Filesystem) Support(ctx context.Context, labels map[string]string) bool {
	_, dataOk := labels[label.NydusDataLayer]
	return dataOk
}

func (fs *Filesystem) StargzLayer(labels map[string]string) bool {
	return labels[label.StargzLayer] != ""
}

func isRafsV6(buf []byte) bool {
	return nativeEndian.Uint32(buf[RafsV6SuppeOffset:]) == RafsV6Magic
}

func getBootstrapRealSizeInV6(buf []byte) uint64 {
	return nativeEndian.Uint64(buf[RafsV6ChunkInfoOffset:])
}

func writeBootstrapToFile(reader io.Reader, bootstrap *os.File, LegacyBootstrap *os.File) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("read bootstrap duration %d", duration.Milliseconds())
	}()
	rd, err := gzip.NewReader(reader)
	if err != nil {
		return errors.Wrap(err, "gzip new reader faield")
	}
	found := false
	tr := tar.NewReader(rd)
	var finalBootstarp *os.File
	for {
		h, err := tr.Next()
		if err != nil {
			return errors.Wrap(err, "can't get next tar entry")
		}
		if h.Name == BootstrapFile {
			found = true
			finalBootstarp = bootstrap
			break
		}

		if h.Name == LegacyBootstrapFile {
			found = true
			finalBootstarp = LegacyBootstrap
			break
		}
	}

	if !found {
		return fmt.Errorf("not found file image.boot in targz")
	}
	buf := make([]byte, MaxSuperBlockSize)
	_, err = tr.Read(buf)
	if err != nil {
		return errors.Wrap(err, "read max super block size from bootstrap file failed")
	}
	_, err = finalBootstarp.Write(buf)

	if err != nil {
		return errors.Wrap(err, "write to bootstrap file failed")
	}

	if isRafsV6(buf) {
		size := getBootstrapRealSizeInV6(buf)
		if size < MaxSuperBlockSize {
			return fmt.Errorf("invalid bootstrap size %d", size)
		}
		// The content of the chunkinfo part is not needed in the v6 format, so it is discarded here.
		_, err := io.CopyN(finalBootstarp, tr, int64(size-MaxSuperBlockSize))
		return err
	}

	// Copy remain data to bootstrap file
	_, err = io.Copy(finalBootstarp, tr)
	return err
}

func (fs *Filesystem) PrepareMetaLayer(ctx context.Context, s storage.Snapshot, labels map[string]string) error {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.G(ctx).Infof("total nydus prepare layer duration %d", duration.Milliseconds())
	}()
	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return fmt.Errorf("can not find ref and digest from label %+v", labels)
	}

	readerCloser, err := fs.resolver.Resolve(ref, layerDigest, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to resolve from ref %s, digest %s", ref, layerDigest)
	}
	defer readerCloser.Close()

	workdir := filepath.Join(fs.UpperPath(s.ID), BootstrapFile)
	legacy := filepath.Join(fs.UpperPath(s.ID), LegacyBootstrapFile)
	err = os.Mkdir(filepath.Dir(workdir), 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create bootstrap dir")
	}
	nydusBootstrap, err := os.OpenFile(workdir, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create bootstrap file")
	}

	legacyNydusBootstrap, err := os.OpenFile(legacy, os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create legacy bootstrap file")
	}

	defer func() {
		closeEmptyFile := []struct {
			file *os.File
			path string
		}{
			{
				file: nydusBootstrap,
				path: workdir,
			},
			{
				file: legacyNydusBootstrap,
				path: legacy,
			},
		}
		for _, in := range closeEmptyFile {
			size, err := in.file.Seek(0, io.SeekEnd)
			if err != nil {
				log.G(ctx).Warnf("failed to seek bootstrap %s file, error %s", workdir, err)
			}
			in.file.Close()
			if size == 0 {
				os.Remove(in.path)
			}
		}
	}()
	log.G(ctx).Infof("prepare write to bootstrap to %s", workdir)
	return writeBootstrapToFile(readerCloser, nydusBootstrap, legacyNydusBootstrap)
}

// Mount will be called when containerd snapshotter prepare remote snapshotter
// this method will fork nydus daemon and manage it in the internal store, and indexed by snapshotID
func (fs *Filesystem) Mount(ctx context.Context, snapshotID string, labels map[string]string) (err error) {
	// If NoneDaemon mode, we don't mount nydus on host
	if fs.mode == NoneInstance {
		return nil
	}

	imageID, ok := labels[label.CRIImageRef]
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
func (fs *Filesystem) WaitUntilReady(ctx context.Context, snapshotID string) error {
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

func (fs *Filesystem) Umount(ctx context.Context, mountPoint string) error {
	if fs.mode == NoneInstance {
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

func (fs *Filesystem) Cleanup(ctx context.Context) error {
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

func (fs *Filesystem) MountPoint(snapshotID string) (string, error) {
	if !fs.hasDaemon() {
		// For NoneDaemon mode, just return error to use snapshotter
		// default mount point path
		return "", fmt.Errorf("don't need nydus daemon of snapshot %s", snapshotID)
	}

	if d, err := fs.manager.GetBySnapshotID(snapshotID); err == nil {
		if fs.mode == SharedInstance {
			return d.SharedMountPoint(), nil
		}
		return d.MountPoint(), nil
	}
	return "", fmt.Errorf("failed to find nydus mountpoint of snapshot %s", snapshotID)
}

func (fs *Filesystem) BootstrapFile(id string) (string, error) {
	return daemon.GetBootstrapFile(fs.SnapshotRoot(), id)
}

func (fs *Filesystem) NewDaemonConfig(labels map[string]string, snapshotID string) (config.DaemonConfig, error) {
	imageID, ok := labels[label.CRIImageRef]
	if !ok {
		return config.DaemonConfig{}, fmt.Errorf("no image ID found in label")
	}

	cfg, err := config.NewDaemonConfig(fs.fsDriver, fs.daemonCfg, imageID, snapshotID, fs.vpcRegistry, labels)
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

func (fs *Filesystem) mount(d *daemon.Daemon, labels map[string]string) error {
	err := fs.generateDaemonConfig(d, labels)
	if err != nil {
		return err
	}
	if fs.mode == SharedInstance || fs.mode == PrefetchInstance {
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

func (fs *Filesystem) addSnapshot(imageID string, labels map[string]string) error {
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

func (fs *Filesystem) initSharedDaemon(ctx context.Context) (_ *daemon.Daemon, retErr error) {
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

func (fs *Filesystem) newDaemon(ctx context.Context, snapshotID string, imageID string) (_ *daemon.Daemon, retErr error) {
	if fs.mode == SharedInstance || fs.mode == PrefetchInstance {
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
			if fs.mode != PrefetchInstance {
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
func (fs *Filesystem) getSharedDaemon() (*daemon.Daemon, error) {
	if fs.sharedDaemon != nil {
		return fs.sharedDaemon, nil
	}

	d, err := fs.manager.GetByID(daemon.SharedNydusDaemonID)
	return d, err
}

// createNewDaemon create new nydus daemon by snapshotID and imageID
func (fs *Filesystem) createNewDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
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
		daemon.WithFsDriver(fs.fsDriver),
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
func (fs *Filesystem) createSharedDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
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
		daemon.WithFsDriver(fs.fsDriver),
	); err != nil {
		return nil, err
	}
	if err = fs.manager.NewDaemon(d); err != nil {
		return nil, err
	}
	return d, nil
}

// generateDaemonConfig generate Daemon configuration
func (fs *Filesystem) generateDaemonConfig(d *daemon.Daemon, labels map[string]string) error {
	cfg, err := config.NewDaemonConfig(d.FsDriver, fs.daemonCfg, d.ImageID, d.SnapshotID, fs.vpcRegistry, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to generate daemon config for daemon %s", d.ID)
	}

	if d.FsDriver == config.FsDriverFscache {
		cfg.Config.CacheConfig.WorkDir = d.FscacheWorkDir()
		bootstrapPath, err := d.BootstrapFile()
		if err != nil {
			return errors.Wrap(err, "get bootstrap path")
		}
		cfg.Config.MetadataPath = bootstrapPath
		cfg.FscacheDaemonConfig.FSPrefetch = cfg.FSPrefetch
		return config.SaveConfig(cfg.FscacheDaemonConfig, d.ConfigFile())
	}

	if fs.cacheMgr != nil {
		// Overriding work_dir option of nyudsd config as we want to set it
		// via snapshotter config option to let snapshotter handle blob cache GC.
		cfg.Device.Cache.Config.WorkDir = fs.cacheMgr.CacheDir()
	}
	return config.SaveConfig(cfg, d.ConfigFile())
}

func (fs *Filesystem) hasDaemon() bool {
	return fs.mode != NoneInstance && fs.mode != PrefetchInstance
}

func (fs *Filesystem) getBlobIDs(labels map[string]string) ([]string, error) {
	idStr, ok := labels[LayerAnnotationNydusBlobIDs]
	if !ok {
		// FIXME: for stargz layer, just return empty blobs here, we
		// need to rethink how to implement blob cache GC for stargz.
		if fs.StargzLayer(labels) {
			return nil, nil
		}
		return nil, errors.New("no blob ids found")
	}
	var result []string
	if err := json.Unmarshal([]byte(idStr), &result); err != nil {
		return nil, err
	}
	return result, nil
}
