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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/pkg/archive/compression"
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
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

const (
	TarfsStatusInit    = 0
	TarfsStatusPrepare = 1
	TarfsStatusReady   = 2
	TarfsStatusFailed  = 3
)

const (
	MaxManifestConfigSize   = 0x100000
	TarfsLayerBootstrapName = "layer.boot"
	TarfsImageBootstrapName = "image.boot"
	TarfsLayerDiskName      = "layer.disk"
	TarfsImageDiskName      = "image.disk"
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

type snapshotStatus struct {
	mutex           sync.Mutex
	status          int
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
	manifest, err := t.fetchImageManifest(ctx, remote, ref, manifestDigest)
	if err != nil {
		return err
	}
	config, err := t.fetchImageConfig(ctx, remote, ref, &manifest)
	if err != nil {
		return err
	}

	if t.checkTarfsHint {
		// cache ref & tarfs hint annotation
		t.tarfsHintCache.Add(ref, label.HasTarfsHint(manifest.Annotations))
	}
	if t.validateDiffID {
		// cache OCI blob digest & diff id
		for i := range manifest.Layers {
			t.diffIDCache.Add(manifest.Layers[i].Digest, config.RootFS.DiffIDs[i])
		}
	}

	return nil
}

func (t *Manager) fetchImageManifest(ctx context.Context, remote *remote.Remote, ref string, manifestDigest digest.Digest) (ocispec.Manifest, error) {
	rc, desc, err := t.getBlobStream(ctx, remote, ref, manifestDigest)
	if err != nil {
		return ocispec.Manifest{}, err
	}
	defer rc.Close()
	if desc.Size > MaxManifestConfigSize {
		return ocispec.Manifest{}, errors.Errorf("image manifest content size %x is too big", desc.Size)
	}
	bytes, err := io.ReadAll(rc)
	if err != nil {
		return ocispec.Manifest{}, errors.Wrap(err, "read image manifest content")
	}

	var manifestOCI ocispec.Manifest
	if err := json.Unmarshal(bytes, &manifestOCI); err != nil {
		return ocispec.Manifest{}, errors.Wrap(err, "unmarshal OCI image manifest")
	}
	if len(manifestOCI.Layers) < 1 {
		return ocispec.Manifest{}, errors.Errorf("invalid OCI image manifest without any layer")
	}

	return manifestOCI, nil
}

func (t *Manager) fetchImageConfig(ctx context.Context, remote *remote.Remote, ref string, manifest *ocispec.Manifest) (ocispec.Image, error) {
	// fetch image config content and extract diffIDs
	rc, desc, err := t.getBlobStream(ctx, remote, ref, manifest.Config.Digest)
	if err != nil {
		return ocispec.Image{}, errors.Wrap(err, "fetch image config content")
	}
	defer rc.Close()
	if desc.Size > MaxManifestConfigSize {
		return ocispec.Image{}, errors.Errorf("image config content size %x is too big", desc.Size)
	}
	bytes, err := io.ReadAll(rc)
	if err != nil {
		return ocispec.Image{}, errors.Wrap(err, "read image config content")
	}

	var config ocispec.Image
	if err := json.Unmarshal(bytes, &config); err != nil {
		return ocispec.Image{}, errors.Wrap(err, "unmarshal image config")
	}
	if len(config.RootFS.DiffIDs) != len(manifest.Layers) {
		return ocispec.Image{}, errors.Errorf("number of diffIDs does not match manifest layers")
	}

	return config, nil
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
func (t *Manager) generateBootstrap(tarReader io.Reader, snapshotID, layerBlobID, upperDirPath string) (err error) {
	snapshotImageDir := filepath.Join(upperDirPath, "image")
	if err := os.MkdirAll(snapshotImageDir, 0750); err != nil {
		return errors.Wrapf(err, "create data dir %s for tarfs snapshot", snapshotImageDir)
	}
	layerMetaFile := t.layerMetaFilePath(upperDirPath)
	if _, err := os.Stat(layerMetaFile); err == nil {
		return errdefs.ErrAlreadyExists
	}
	layerMetaFileTmp := layerMetaFile + ".tarfs.tmp"
	defer os.Remove(layerMetaFileTmp)

	layerTarFile := t.layerTarFilePath(layerBlobID)
	layerTarFileTmp := layerTarFile + ".tarfs.tmp"
	tarFile, err := os.Create(layerTarFileTmp)
	if err != nil {
		return errors.Wrap(err, "create temporary file to store tar stream")
	}
	defer tarFile.Close()
	defer os.Remove(layerTarFileTmp)

	fifoName := filepath.Join(upperDirPath, "layer_"+snapshotID+"_"+"tar.fifo")
	if err = syscall.Mkfifo(fifoName, 0644); err != nil {
		return err
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
		return errors.Wrap(err, "converting OCIv1 layer blob to tarfs")
	}
	log.L.Debugf("nydus image output %s", outb.String())
	log.L.Debugf("nydus image err %s", errb.String())

	if err := os.Rename(layerTarFileTmp, layerTarFile); err != nil {
		return errors.Wrapf(err, "rename file %s to %s", layerTarFileTmp, layerTarFile)
	}
	if err := os.Rename(layerMetaFileTmp, layerMetaFile); err != nil {
		return errors.Wrapf(err, "rename file %s to %s", layerMetaFileTmp, layerMetaFile)
	}

	return nil
}

func (t *Manager) getImageBlobInfo(metaFilePath string) (string, error) {
	if _, err := os.Stat(metaFilePath); err != nil {
		return "", err
	}

	options := []string{
		"inspect",
		"-R blobs",
		metaFilePath,
	}
	cmd := exec.Command(t.nydusImagePath, options...)
	var errb, outb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = &outb
	log.L.Debugf("nydus image command %v", options)
	err := cmd.Run()
	if err != nil {
		log.L.Warnf("nydus image exec failed, %s", errb.String())
		return "", errors.Wrap(err, "converting OCIv1 layer blob to tarfs")
	}

	return outb.String(), nil
}

// download & uncompress an oci/docker blob, and then generate the tarfs bootstrap
func (t *Manager) blobProcess(ctx context.Context, wg *sync.WaitGroup, snapshotID, ref string,
	manifestDigest, layerDigest digest.Digest, upperDirPath string) error {
	layerBlobID := layerDigest.Hex()
	epilog := func(err error, msg string) {
		st, err1 := t.getSnapshotStatus(snapshotID, true)
		if err1 != nil {
			// return errors.Errorf("can not found status object for snapshot %s after prepare", snapshotID)
			err1 = errors.Wrapf(err1, "can not found status object for snapshot %s after prepare", snapshotID)
			log.L.WithError(err1).Errorf("async prepare tarfs layer for snapshot ID %s", snapshotID)
			return
		}
		defer st.mutex.Unlock()

		st.blobID = layerBlobID
		st.blobTarFilePath = t.layerTarFilePath(layerBlobID)
		if err != nil {
			log.L.WithError(err).Error(msg)
			st.status = TarfsStatusFailed
		} else {
			st.status = TarfsStatusReady
		}
		log.L.Info(msg)
	}

	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		epilog(err, "create key chain for connection")
		return err
	}
	remote := remote.New(keyChain, t.insecure)
	rc, _, err := t.getBlobStream(ctx, remote, ref, layerDigest)
	if err != nil && remote.RetryWithPlainHTTP(ref, err) {
		rc, _, err = t.getBlobStream(ctx, remote, ref, layerDigest)
	}
	if err != nil {
		epilog(err, "get blob stream for layer")
		return errors.Wrapf(err, "get blob stream by digest")
	}

	go func() {
		defer wg.Done()
		defer rc.Close()

		ds, err := compression.DecompressStream(rc)
		if err != nil {
			epilog(err, "unpack layer blob stream for tarfs")
			return
		}
		defer ds.Close()

		if t.validateDiffID {
			diffID, err := t.getBlobDiffID(ctx, remote, ref, manifestDigest, layerDigest)
			if err != nil {
				epilog(err, "get layer diffID")
				return
			}
			digester := digest.Canonical.Digester()
			dr := io.TeeReader(ds, digester.Hash())
			err = t.generateBootstrap(dr, snapshotID, layerBlobID, upperDirPath)
			switch {
			case err != nil && !errdefs.IsAlreadyExists(err):
				epilog(err, "generate tarfs from image layer blob")
			case err == nil && digester.Digest() != diffID:
				epilog(err, "image layer diffID does not match")
			default:
				msg := fmt.Sprintf("nydus tarfs for snapshot %s is ready, digest %s", snapshotID, digester.Digest())
				epilog(nil, msg)
			}
		} else {
			err = t.generateBootstrap(ds, snapshotID, layerBlobID, upperDirPath)
			if err != nil && !errdefs.IsAlreadyExists(err) {
				epilog(err, "generate tarfs data from image layer blob")
			} else {
				msg := fmt.Sprintf("nydus tarfs for snapshot %s is ready", snapshotID)
				epilog(nil, msg)
			}
		}
	}()

	return err
}

func (t *Manager) PrepareLayer(snapshotID, ref string, manifestDigest, layerDigest digest.Digest, upperDirPath string) error {
	t.mutex.Lock()
	if _, ok := t.snapshotMap[snapshotID]; ok {
		t.mutex.Unlock()
		return errors.Errorf("snapshot %s has already been prapared", snapshotID)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())

	t.snapshotMap[snapshotID] = &snapshotStatus{
		status: TarfsStatusPrepare,
		wg:     wg,
		cancel: cancel,
	}
	t.mutex.Unlock()

	return t.blobProcess(ctx, wg, snapshotID, ref, manifestDigest, layerDigest, upperDirPath)
}

func (t *Manager) MergeLayers(s storage.Snapshot, storageLocater func(string) string) error {
	mergedBootstrap := t.imageMetaFilePath(storageLocater(s.ParentIDs[0]))
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

func (t *Manager) ExportBlockData(s storage.Snapshot, perLayer bool, labels map[string]string, storageLocater func(string) string) ([]string, error) {
	updateFields := []string{}

	wholeImage, exportDisk, withVerity := config.GetTarfsExportFlags()

	log.L.Debugf("ExportBlockData wholeImage = %v, exportDisk = %v, withVerity = %v, perLayer = %v", wholeImage, exportDisk, withVerity, perLayer)
	// Nothing to do for this case, all needed datum are ready.
	if !exportDisk && !withVerity {
		return updateFields, nil
	} else if !wholeImage != perLayer {
		// Special handling for `layer_block` mode
		if exportDisk && !withVerity && !perLayer {
			labels[label.NydusLayerBlockInfo] = ""
			updateFields = append(updateFields, "labels."+label.NydusLayerBlockInfo)
		}
		return updateFields, nil
	}

	var snapshotID string
	if perLayer {
		snapshotID = s.ID
	} else {
		if len(s.ParentIDs) == 0 {
			return updateFields, errors.Errorf("snapshot %s has no parent", s.ID)
		}
		snapshotID = s.ParentIDs[0]
	}
	err := t.waitLayerReady(snapshotID)
	if err != nil {
		return updateFields, errors.Wrapf(err, "wait for tarfs snapshot %s to get ready", snapshotID)
	}
	st, err := t.getSnapshotStatus(snapshotID, false)
	if err != nil {
		return updateFields, err
	}
	if st.status != TarfsStatusReady {
		return updateFields, errors.Errorf("tarfs snapshot %s is not ready, %d", snapshotID, st.status)
	}

	blobID, ok := labels[label.NydusTarfsLayer]
	if !ok {
		return updateFields, errors.Errorf("Missing Nydus tarfs layer annotation for snapshot %s", s.ID)
	}

	var metaFileName, diskFileName string
	if wholeImage {
		metaFileName = t.imageMetaFilePath(storageLocater(snapshotID))
		diskFileName = t.ImageDiskFilePath(blobID)
	} else {
		metaFileName = t.layerMetaFilePath(storageLocater(snapshotID))
		diskFileName = t.LayerDiskFilePath(blobID)
	}

	// Do not regenerate if the disk image already exists.
	if _, err := os.Stat(diskFileName); err == nil {
		return updateFields, nil
	}
	diskFileNameTmp := diskFileName + ".tarfs.tmp"
	defer os.Remove(diskFileNameTmp)

	options := []string{
		"export",
		"--block",
		"--localfs-dir", t.cacheDirPath,
		"--bootstrap", metaFileName,
		"--output", diskFileNameTmp,
	}
	if withVerity {
		options = append(options, "--verity")
	}
	log.L.Debugf("nydus image command %v", options)
	cmd := exec.Command(t.nydusImagePath, options...)
	var errb, outb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Stdout = &outb
	err = cmd.Run()
	if err != nil {
		return updateFields, errors.Wrap(err, "merge tarfs image layers")
	}
	log.L.Debugf("nydus image export command, stdout: %s, stderr: %s", &outb, &errb)

	blockInfo := ""
	if withVerity {
		pattern := "dm-verity options: --no-superblock --format=1 -s \"\" --hash=sha256 --data-block-size=512 --hash-block-size=4096 --data-blocks %d --hash-offset %d %s\n"
		var dataBlobks, hashOffset uint64
		var rootHash string
		if count, err := fmt.Sscanf(outb.String(), pattern, &dataBlobks, &hashOffset, &rootHash); err != nil || count != 3 {
			return updateFields, errors.Errorf("failed to parse dm-verity options from nydus image output: %s", outb.String())
		}
		blockInfo = strconv.FormatUint(dataBlobks, 10) + "," + strconv.FormatUint(hashOffset, 10) + "," + "sha256:" + rootHash
	}
	if wholeImage {
		labels[label.NydusImageBlockInfo] = blockInfo
		updateFields = append(updateFields, "labels."+label.NydusImageBlockInfo)
	} else {
		labels[label.NydusLayerBlockInfo] = blockInfo
		updateFields = append(updateFields, "labels."+label.NydusLayerBlockInfo)
	}
	log.L.Debugf("export block labels %v", labels)

	err = os.Rename(diskFileNameTmp, diskFileName)
	if err != nil {
		return updateFields, errors.Wrap(err, "rename disk image file")
	}

	return updateFields, nil
}

func (t *Manager) MountTarErofs(snapshotID string, s *storage.Snapshot, labels map[string]string, rafs *rafs.Rafs) error {
	if s == nil {
		return errors.New("snapshot object for MountTarErofs() is nil")
	}

	// Copy meta info from snapshot to rafs
	t.copyTarfsAnnotations(labels, rafs)

	upperDirPath := path.Join(rafs.GetSnapshotDir(), "fs")
	if !config.GetTarfsMountOnHost() {
		rafs.SetMountpoint(upperDirPath)
		return nil
	}

	mergedBootstrap := t.imageMetaFilePath(upperDirPath)
	blobInfo, err := t.getImageBlobInfo(mergedBootstrap)
	if err != nil {
		return errors.Wrapf(err, "get image blob info")
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

		var blobMarker = "\"blob_id\":\"" + st.blobID + "\""
		if strings.Contains(blobInfo, blobMarker) {
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
		st.erofsMountPoint = ""
	}

	return nil
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
		st.erofsMountPoint = ""
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

func (t *Manager) waitLayerReady(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID, false)
	if err != nil {
		return err
	}
	if st.status != TarfsStatusReady {
		log.L.Debugf("wait tarfs conversion task for snapshot %s", snapshotID)
	}
	st.wg.Wait()
	if st.status != TarfsStatusReady {
		return errors.Errorf("snapshot %s is in state %d instead of ready state", snapshotID, st.status)
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

func (t *Manager) copyTarfsAnnotations(labels map[string]string, rafs *rafs.Rafs) {
	keys := []string{
		label.NydusTarfsLayer,
		label.NydusImageBlockInfo,
		label.NydusLayerBlockInfo,
	}

	for _, k := range keys {
		if v, ok := labels[k]; ok {
			rafs.AddAnnotation(k, v)
		}
	}
}

func (t *Manager) layerTarFilePath(blobID string) string {
	return filepath.Join(t.cacheDirPath, blobID)
}

func (t *Manager) LayerDiskFilePath(blobID string) string {
	return filepath.Join(t.cacheDirPath, blobID+"."+TarfsLayerDiskName)
}

func (t *Manager) ImageDiskFilePath(blobID string) string {
	return filepath.Join(t.cacheDirPath, blobID+"."+TarfsImageDiskName)
}

func (t *Manager) layerMetaFilePath(upperDirPath string) string {
	return filepath.Join(upperDirPath, "image", TarfsLayerBootstrapName)
}

func (t *Manager) imageMetaFilePath(upperDirPath string) string {
	return filepath.Join(upperDirPath, "image", TarfsImageBootstrapName)
}
