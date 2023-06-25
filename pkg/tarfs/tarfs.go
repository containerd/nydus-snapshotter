/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package tarfs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	losetup "github.com/freddierice/go-losetup"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/unix"
	"k8s.io/utils/lru"
)

type Manager struct {
	snapshotMap          map[string]*snapshotStatus // tarfs snapshots status, indexed by snapshot ID
	mutex                sync.Mutex
	mutexLoopDev         sync.Mutex
	cacheDirPath         string
	nydusImagePath       string
	insecure             bool
	validateDiffID       bool // whether to validate digest for uncompressed content
	checkTarfsHint       bool // whether to rely on tarfs hint annotation
	maxConcurrentProcess int64
	processLimiterCache  *lru.Cache // cache image ref and concurrent limiter for blob processes
	tarfsHintCache       *lru.Cache // cache oci image ref and tarfs hint annotation
	diffIDCache          *lru.Cache // cache oci blob digest and diffID
	sg                   singleflight.Group
}

const (
	TarfsStatusInit    = 0
	TarfsStatusPrepare = 1
	TarfsStatusReady   = 2
	TarfsStatusFailed  = 3
)

const (
	MaxManifestConfigSize    = 0x100000
	TarfsLayerBootstapName   = "layer.boot"
	TarfsMeragedBootstapName = "image.boot"
)

var ErrEmptyBlob = errors.New("empty blob")

type snapshotStatus struct {
	mutex           sync.Mutex
	status          int
	isEmptyBlob     bool
	blobID          string
	blobTarFilePath string
	erofsMountPoint string
	dataLoopdev     *losetup.Device
	metaLoopdev     *losetup.Device
	wg              *sync.WaitGroup
	cancel          context.CancelFunc
}

func NewManager(insecure, checkTarfsHint bool, cacheDirPath, nydusImagePath string, maxConcurrentProcess int64) *Manager {
	return &Manager{
		snapshotMap:          map[string]*snapshotStatus{},
		cacheDirPath:         cacheDirPath,
		nydusImagePath:       nydusImagePath,
		insecure:             insecure,
		validateDiffID:       true,
		checkTarfsHint:       checkTarfsHint,
		maxConcurrentProcess: maxConcurrentProcess,
		tarfsHintCache:       lru.New(50),
		processLimiterCache:  lru.New(50),
		diffIDCache:          lru.New(1000),
		sg:                   singleflight.Group{},
	}
}

// Fetch image manifest and config contents, cache frequently used information.
// FIXME need an update policy
func (t *Manager) fetchImageInfo(ctx context.Context, remote *remote.Remote, ref string, manifestDigest digest.Digest) error {
	// fetch image manifest content
	rc, desc, err := t.getBlobStream(ctx, remote, ref, manifestDigest)
	if err != nil {
		return err
	}
	defer rc.Close()
	if desc.Size > MaxManifestConfigSize {
		return errors.Errorf("image manifest content size %x is too big", desc.Size)
	}
	bytes, err := io.ReadAll(rc)
	if err != nil {
		return errors.Wrap(err, "read image manifest content")
	}

	// TODO: support docker v2 images
	var manifestOCI ocispec.Manifest
	if err := json.Unmarshal(bytes, &manifestOCI); err != nil {
		return errors.Wrap(err, "unmarshal OCI image manifest")
	}
	if len(manifestOCI.Layers) < 1 {
		return errors.Errorf("invalid OCI image manifest without any layer")
	}

	// fetch image config content and extract diffIDs
	rc, desc, err = t.getBlobStream(ctx, remote, ref, manifestOCI.Config.Digest)
	if err != nil {
		return errors.Wrap(err, "fetch image config content")
	}
	defer rc.Close()
	if desc.Size > MaxManifestConfigSize {
		return errors.Errorf("image config content size %x is too big", desc.Size)
	}
	bytes, err = io.ReadAll(rc)
	if err != nil {
		return errors.Wrap(err, "read image config content")
	}

	var config ocispec.Image
	if err := json.Unmarshal(bytes, &config); err != nil {
		return errors.Wrap(err, "unmarshal image config")
	}
	if len(config.RootFS.DiffIDs) != len(manifestOCI.Layers) {
		return errors.Errorf("number of diffIDs does not match manifest layers")
	}

	if t.checkTarfsHint {
		// cache ref & tarfs hint annotation
		t.tarfsHintCache.Add(ref, label.HasTarfsHint(manifestOCI.Annotations))
	}
	if t.validateDiffID {
		// cache OCI blob digest & diff id
		for i := range manifestOCI.Layers {
			t.diffIDCache.Add(manifestOCI.Layers[i].Digest, config.RootFS.DiffIDs[i])
		}
	}
	return nil
}

func (t *Manager) getBlobDiffID(ctx context.Context, remote *remote.Remote, ref string, manifestDigest, layerDigest digest.Digest) (digest.Digest, error) {
	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(digest.Digest), nil
	}

	if _, err, _ := t.sg.Do(ref, func() (interface{}, error) {
		err := t.fetchImageInfo(ctx, remote, ref, manifestDigest)
		return nil, err
	}); err != nil {
		return "", err
	}

	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(digest.Digest), nil
	}

	return "", errors.Errorf("get blob diff id failed")
}

func (t *Manager) getBlobStream(ctx context.Context, remote *remote.Remote, ref string, contentDigest digest.Digest) (io.ReadCloser, ocispec.Descriptor, error) {
	fetcher, err := remote.Fetcher(ctx, ref)
	if err != nil {
		return nil, ocispec.Descriptor{}, errors.Wrap(err, "get remote fetcher")
	}

	fetcherByDigest, ok := fetcher.(remotes.FetcherByDigest)
	if !ok {
		return nil, ocispec.Descriptor{}, errors.Errorf("fetcher %T does not implement remotes.FetcherByDigest", fetcher)
	}

	return fetcherByDigest.FetchByDigest(ctx, contentDigest)
}

// generate tar file and layer bootstrap, return if this blob is an empty blob
func (t *Manager) generateBootstrap(tarReader io.Reader, snapshotID, layerBlobID, upperDirPath string) (emptyBlob bool, err error) {
	snapshotImageDir := filepath.Join(upperDirPath, "image")
	if err := os.MkdirAll(snapshotImageDir, 0750); err != nil {
		return false, errors.Wrapf(err, "create data dir %s for tarfs snapshot", snapshotImageDir)
	}
	layerMetaFile := t.layerMetaFilePath(upperDirPath)
	if _, err := os.Stat(layerMetaFile); err == nil {
		return false, errors.Errorf("tarfs bootstrap file %s for snapshot %s already exists", layerMetaFile, snapshotID)
	}
	layerMetaFileTmp := layerMetaFile + ".tarfs.tmp"
	defer os.Remove(layerMetaFileTmp)

	layerTarFile := t.layerTarFilePath(layerBlobID)
	layerTarFileTmp := layerTarFile + ".tarfs.tmp"
	tarFile, err := os.Create(layerTarFileTmp)
	if err != nil {
		return false, errors.Wrap(err, "create temporary file to store tar stream")
	}
	defer tarFile.Close()
	defer os.Remove(layerTarFileTmp)

	fifoName := filepath.Join(upperDirPath, "layer_"+snapshotID+"_"+"tar.fifo")
	if err = syscall.Mkfifo(fifoName, 0644); err != nil {
		return false, err
	}
	defer os.Remove(fifoName)

	go func() {
		fifoFile, err := os.OpenFile(fifoName, os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
			log.L.Warnf("can not open fifo file,  err %v", err)
			return
		}
		defer fifoFile.Close()
		if _, err := io.Copy(fifoFile, io.TeeReader(tarReader, tarFile)); err != nil {
			log.L.Warnf("tar stream copy err %v", err)
		}
	}()

	options := []string{
		"create",
		"--type", "tar-tarfs",
		"--bootstrap", layerMetaFileTmp,
		"--blob-id", layerBlobID,
		"--blob-dir", t.cacheDirPath,
		fifoName,
	}
	cmd := exec.Command(t.nydusImagePath, options...)
	var errb, outb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = &outb
	log.L.Debugf("nydus image command %v", options)
	err = cmd.Run()
	if err != nil {
		log.L.Warnf("nydus image exec failed, %s", errb.String())
		return false, errors.Wrap(err, "converting OCIv1 layer blob to tarfs")
	}
	log.L.Debugf("nydus image output %s", outb.String())
	log.L.Debugf("nydus image err %s", errb.String())

	if err := os.Rename(layerTarFileTmp, layerTarFile); err != nil {
		return false, errors.Wrapf(err, "rename file %s to %s", layerTarFileTmp, layerTarFile)
	}
	if err := os.Rename(layerMetaFileTmp, layerMetaFile); err != nil {
		return false, errors.Wrapf(err, "rename file %s to %s", layerMetaFileTmp, layerMetaFile)
	}

	// TODO need a more reliable way to check if this is an empty blob
	if strings.Contains(outb.String(), "data blob size: 0x0\n") ||
		strings.Contains(errb.String(), "data blob size: 0x0\n") {
		return true, nil
	}
	return false, nil
}

// download & uncompress an oci/docker blob, and then generate the tarfs bootstrap
func (t *Manager) blobProcess(ctx context.Context, snapshotID, ref string, manifestDigest, layerDigest digest.Digest,
	layerBlobID, upperDirPath string) error {
	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return err
	}
	remote := remote.New(keyChain, t.insecure)

	handle := func() (bool, error) {
		rc, _, err := t.getBlobStream(ctx, remote, ref, layerDigest)
		if err != nil {
			return false, err
		}
		defer rc.Close()
		ds, err := compression.DecompressStream(rc)
		if err != nil {
			return false, errors.Wrap(err, "unpack layer blob stream for tarfs")
		}
		defer ds.Close()

		var emptyBlob bool
		if t.validateDiffID {
			diffID, err := t.getBlobDiffID(ctx, remote, ref, manifestDigest, layerDigest)
			if err != nil {
				return false, errors.Wrap(err, "get image layer diffID")
			}
			digester := digest.Canonical.Digester()
			dr := io.TeeReader(ds, digester.Hash())
			emptyBlob, err = t.generateBootstrap(dr, snapshotID, layerBlobID, upperDirPath)
			if err != nil {
				return false, errors.Wrap(err, "generate tarfs data from image layer blob")
			}
			if digester.Digest() != diffID {
				return false, errors.Errorf("image layer diffID %s for tarfs does not match", diffID)
			}
			log.L.Infof("tarfs data for layer %s is ready, digest %s", snapshotID, digester.Digest())
		} else {
			emptyBlob, err = t.generateBootstrap(ds, snapshotID, layerBlobID, upperDirPath)
			if err != nil {
				return false, errors.Wrap(err, "generate tarfs data from image layer blob")
			}
			log.L.Infof("tarfs data for layer %s is ready", snapshotID)
		}
		return emptyBlob, nil
	}

	var emptyBlob bool
	emptyBlob, err = handle()
	if err != nil && remote.RetryWithPlainHTTP(ref, err) {
		emptyBlob, err = handle()
	}
	if err != nil {
		return err
	}
	if emptyBlob {
		return ErrEmptyBlob
	}
	return nil
}

func (t *Manager) PrepareLayer(snapshotID, ref string, manifestDigest, layerDigest digest.Digest,
	upperDirPath string) error {
	t.mutex.Lock()
	if _, ok := t.snapshotMap[snapshotID]; ok {
		t.mutex.Unlock()
		return errors.Errorf("snapshot %s has already been prapared", snapshotID)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	defer wg.Done()
	ctx, cancel := context.WithCancel(context.Background())

	t.snapshotMap[snapshotID] = &snapshotStatus{
		status: TarfsStatusPrepare,
		wg:     wg,
		cancel: cancel,
	}
	t.mutex.Unlock()

	layerBlobID := layerDigest.Hex()
	err := t.blobProcess(ctx, snapshotID, ref, manifestDigest, layerDigest, layerBlobID, upperDirPath)

	st, err1 := t.getSnapshotStatus(snapshotID, true)
	if err1 != nil {
		return errors.Errorf("can not found status object for snapshot %s after prepare", snapshotID)
	}
	defer st.mutex.Unlock()

	st.blobID = layerBlobID
	st.blobTarFilePath = t.layerTarFilePath(layerBlobID)
	if err != nil {
		if errors.Is(err, ErrEmptyBlob) {
			st.isEmptyBlob = true
			st.status = TarfsStatusReady
			err = nil
		} else {
			st.status = TarfsStatusFailed
		}
	} else {
		st.isEmptyBlob = false
		st.status = TarfsStatusReady
	}
	log.L.Debugf("finish converting snapshot %s to tarfs, status %d, empty blob %v", snapshotID, st.status, st.isEmptyBlob)

	return err
}

func (t *Manager) MergeLayers(s storage.Snapshot, storageLocater func(string) string) error {
	mergedBootstrap := t.mergedMetaFilePath(storageLocater(s.ParentIDs[0]))
	if _, err := os.Stat(mergedBootstrap); err == nil {
		log.L.Debugf("tarfs snapshot %s already has merged bootstrap %s", s.ParentIDs[0], mergedBootstrap)
		return nil
	}

	bootstraps := []string{}
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		err := t.waitLayerReady(snapshotID)
		if err != nil {
			return errors.Wrapf(err, "wait for tarfs snapshot %s to get ready", snapshotID)
		}

		st, err := t.getSnapshotStatus(snapshotID, false)
		if err != nil {
			return err
		}
		if st.status != TarfsStatusReady {
			return errors.Errorf("tarfs snapshot %s is not ready, %d", snapshotID, st.status)
		}

		metaFilePath := t.layerMetaFilePath(storageLocater(snapshotID))
		bootstraps = append(bootstraps, metaFilePath)
	}

	mergedBootstrapTmp := mergedBootstrap + ".tarfs.tmp"
	defer os.Remove(mergedBootstrapTmp)

	options := []string{
		"merge",
		"--bootstrap", mergedBootstrapTmp,
	}
	options = append(options, bootstraps...)
	cmd := exec.Command(t.nydusImagePath, options...)
	var errb, outb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = &outb
	log.L.Debugf("nydus image command %v", options)
	err := cmd.Run()
	if err != nil {
		return errors.Wrap(err, "merge tarfs image layers")
	}

	err = os.Rename(mergedBootstrapTmp, mergedBootstrap)
	if err != nil {
		return errors.Wrap(err, "rename merged bootstrap file")
	}

	return nil
}

func (t *Manager) attachLoopdev(blob string) (*losetup.Device, error) {
	// losetup.Attach() is not thread-safe hold lock here
	t.mutexLoopDev.Lock()
	defer t.mutexLoopDev.Unlock()
	dev, err := losetup.Attach(blob, 0, false)
	return &dev, err
}

func (t *Manager) MountTarErofs(snapshotID string, s *storage.Snapshot, rafs *daemon.Rafs) error {
	if s == nil {
		return errors.New("snapshot object for MountTarErofs() is nil")
	}

	var devices []string
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		err := t.waitLayerReady(snapshotID)
		if err != nil {
			return errors.Wrapf(err, "wait for tarfs conversion task")
		}

		st, err := t.getSnapshotStatus(snapshotID, true)
		if err != nil {
			return err
		}
		if st.status != TarfsStatusReady {
			st.mutex.Unlock()
			return errors.Errorf("snapshot %s tarfs format error %d", snapshotID, st.status)
		}

		if !st.isEmptyBlob {
			if st.dataLoopdev == nil {
				loopdev, err := t.attachLoopdev(st.blobTarFilePath)
				if err != nil {
					st.mutex.Unlock()
					return errors.Wrapf(err, "attach layer tar file %s to loopdev", st.blobTarFilePath)
				}
				st.dataLoopdev = loopdev
			}
			devices = append(devices, "device="+st.dataLoopdev.Path())
		}

		st.mutex.Unlock()
	}
	mountOpts := strings.Join(devices, ",")

	st, err := t.getSnapshotStatus(snapshotID, true)
	if err != nil {
		return err
	}
	defer st.mutex.Unlock()

	mountPoint := path.Join(rafs.GetSnapshotDir(), "mnt")
	if len(st.erofsMountPoint) > 0 {
		if st.erofsMountPoint == mountPoint {
			log.L.Debugf("tarfs for snapshot %s has already been mounted at %s", snapshotID, mountPoint)
			return nil
		}
		return errors.Errorf("tarfs for snapshot %s has already been mounted at %s", snapshotID, st.erofsMountPoint)
	}

	if st.metaLoopdev == nil {
		upperDirPath := path.Join(rafs.GetSnapshotDir(), "fs")
		mergedBootstrap := t.mergedMetaFilePath(upperDirPath)
		loopdev, err := t.attachLoopdev(mergedBootstrap)
		if err != nil {
			return errors.Wrapf(err, "attach merged bootstrap %s to loopdev", mergedBootstrap)
		}
		st.metaLoopdev = loopdev
	}
	devName := st.metaLoopdev.Path()

	if err = os.MkdirAll(mountPoint, 0750); err != nil {
		return errors.Wrapf(err, "create tarfs mount dir %s", mountPoint)
	}

	err = unix.Mount(devName, mountPoint, "erofs", 0, mountOpts)
	if err != nil {
		return errors.Wrapf(err, "mount erofs at %s with opts %s", mountPoint, mountOpts)
	}
	st.erofsMountPoint = mountPoint
	rafs.SetMountpoint(mountPoint)
	return nil
}

func (t *Manager) UmountTarErofs(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID, true)
	if err != nil {
		return errors.Wrapf(err, "umount a tarfs snapshot %s which is already removed", snapshotID)
	}
	defer st.mutex.Unlock()

	if len(st.erofsMountPoint) > 0 {
		err := unix.Unmount(st.erofsMountPoint, 0)
		if err != nil {
			return errors.Wrapf(err, "umount erofs tarfs %s", st.erofsMountPoint)
		}
	}
	st.erofsMountPoint = ""
	return nil
}

func (t *Manager) waitLayerReady(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID, false)
	if err != nil {
		return err
	}
	if st.status != TarfsStatusReady {
		log.L.Debugf("wait tarfs conversion task for snapshot %s", snapshotID)
	}
	st.wg.Wait()
	return nil
}

func (t *Manager) IsTarfsLayer(snapshotID string) bool {
	_, err := t.getSnapshotStatus(snapshotID, false)
	return err == nil
}

// check if a snapshot is tarfs layer and if mounted a erofs tarfs
func (t *Manager) IsMountedTarfsLayer(snapshotID string) bool {
	st, err := t.getSnapshotStatus(snapshotID, true)
	if err != nil {
		return false
	}
	defer st.mutex.Unlock()

	return len(st.erofsMountPoint) != 0
}

func (t *Manager) DetachLayer(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID, true)
	if err != nil {
		return os.ErrNotExist
	}

	if len(st.erofsMountPoint) > 0 {
		err := unix.Unmount(st.erofsMountPoint, 0)
		if err != nil {
			st.mutex.Unlock()
			return errors.Wrapf(err, "umount erofs tarfs %s", st.erofsMountPoint)
		}
	}

	if st.metaLoopdev != nil {
		err := st.metaLoopdev.Detach()
		if err != nil {
			st.mutex.Unlock()
			return errors.Wrapf(err, "detach merged bootstrap loopdev for tarfs snapshot %s", snapshotID)
		}
		st.metaLoopdev = nil
	}

	if st.dataLoopdev != nil {
		err := st.dataLoopdev.Detach()
		if err != nil {
			st.mutex.Unlock()
			return errors.Wrapf(err, "detach layer bootstrap loopdev for tarfs snapshot %s", snapshotID)
		}
		st.dataLoopdev = nil
	}

	st.mutex.Unlock()
	// TODO: check order
	st.cancel()

	t.mutex.Lock()
	delete(t.snapshotMap, snapshotID)
	t.mutex.Unlock()
	return nil
}

func (t *Manager) getSnapshotStatus(snapshotID string, lock bool) (*snapshotStatus, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[snapshotID]
	if ok {
		if lock {
			st.mutex.Lock()
		}
		return st, nil
	}
	return nil, errors.Errorf("not found snapshot %s", snapshotID)
}

func (t *Manager) CheckTarfsHintAnnotation(ctx context.Context, ref string, manifestDigest digest.Digest) (bool, error) {
	if !t.checkTarfsHint {
		return true, nil
	}

	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return false, err
	}
	remote := remote.New(keyChain, t.insecure)

	handle := func() (bool, error) {
		if tarfsHint, ok := t.tarfsHintCache.Get(ref); ok {
			return tarfsHint.(bool), nil
		}

		if _, err, _ := t.sg.Do(ref, func() (interface{}, error) {
			err := t.fetchImageInfo(ctx, remote, ref, manifestDigest)
			return nil, err
		}); err != nil {
			return false, err
		}

		if tarfsHint, ok := t.tarfsHintCache.Get(ref); ok {
			return tarfsHint.(bool), nil
		}

		return false, errors.Errorf("get tarfs hint annotation failed")
	}

	tarfsHint, err := handle()
	if err != nil && remote.RetryWithPlainHTTP(ref, err) {
		tarfsHint, err = handle()
	}
	return tarfsHint, err
}

func (t *Manager) GetConcurrentLimiter(ref string) *semaphore.Weighted {
	if t.maxConcurrentProcess <= 0 {
		return nil
	}

	if limiter, ok := t.processLimiterCache.Get(ref); ok {
		return limiter.(*semaphore.Weighted)
	}

	limiter := semaphore.NewWeighted(t.maxConcurrentProcess)
	t.processLimiterCache.Add(ref, limiter)
	return limiter
}

func (t *Manager) layerTarFilePath(blobID string) string {
	return filepath.Join(t.cacheDirPath, blobID)
}

func (t *Manager) layerMetaFilePath(upperDirPath string) string {
	return filepath.Join(upperDirPath, "image", TarfsLayerBootstapName)
}

func (t *Manager) mergedMetaFilePath(upperDirPath string) string {
	return filepath.Join(upperDirPath, "image", TarfsMeragedBootstapName)
}
