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
	"time"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/rafs"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	"github.com/moby/sys/mountinfo"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/unix"
	"k8s.io/utils/lru"
)

const (
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
	RemountMap           map[string]*rafs.Rafs      // Scratch space to store rafs instances needing remount on startup
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
	erofsMountPoint string
	dataLoopdev     *os.File
	metaLoopdev     *os.File
	wg              *sync.WaitGroup
	cancel          context.CancelFunc
	ctx             context.Context
}

func NewManager(insecure, checkTarfsHint bool, cacheDirPath, nydusImagePath string, maxConcurrentProcess int64) *Manager {
	return &Manager{
		snapshotMap:          map[string]*snapshotStatus{},
		RemountMap:           map[string]*rafs.Rafs{},
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
func (t *Manager) generateBootstrap(tarReader io.Reader, snapshotID, layerBlobID, upperDirPath string, w *sync.WaitGroup) (err error) {
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
	if err = syscall.Mkfifo(fifoName, 0640); err != nil {
		return err
	}
	defer os.Remove(fifoName)

	w.Add(1)
	go func() {
		defer w.Done()

		var fifoFile *os.File
		for i := 1; i < 100 && fifoFile == nil; i++ {
			file, err := os.OpenFile(fifoName, os.O_RDWR, os.ModeNamedPipe)
			switch {
			case err == nil:
				fifoFile = file
			case os.IsNotExist(err) || os.IsPermission(err):
				log.L.Warnf("open fifo file, %v", err)
				return
			default:
				log.L.Warnf("open fifo file, %v", err)
				time.Sleep(time.Duration(i) * 10 * time.Millisecond)
			}
		}
		defer fifoFile.Close()

		if _, err := io.Copy(fifoFile, io.TeeReader(tarReader, tarFile)); err != nil {
			log.L.Warnf("tar stream copy, %v", err)
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
func (t *Manager) blobProcess(ctx context.Context, snapshotID, ref string,
	manifestDigest, layerDigest digest.Digest, upperDirPath string, retry bool) error {
	layerBlobID := layerDigest.Hex()

	epilog := func(err error, msg string) error {
		st, err1 := t.getSnapshotStatusWithLock(snapshotID)
		if err1 != nil {
			return errors.Wrapf(err, "can not found status object for snapshot %s after prepare", snapshotID)
		}
		defer func() {
			if st.wg != nil {
				st.wg.Done()
				st.wg = nil
			}
			st.mutex.Unlock()
		}()

		st.blobID = layerBlobID
		if err != nil {
			st.status = TarfsStatusFailed
		} else {
			st.status = TarfsStatusReady
		}

		return errors.Wrapf(err, msg)
	}

	process := func(rc io.ReadCloser, remote *remote.Remote) error {
		defer rc.Close()

		var w sync.WaitGroup
		defer w.Wait()

		ds, err := compression.DecompressStream(rc)
		if err != nil {
			return epilog(err, "unpack layer blob stream for tarfs")
		}
		defer ds.Close()

		if t.validateDiffID {
			diffID, err := t.getBlobDiffID(ctx, remote, ref, manifestDigest, layerDigest)
			if err != nil {
				return epilog(err, "get layer diffID")
			}
			digester := digest.Canonical.Digester()
			dr := io.TeeReader(ds, digester.Hash())
			err = t.generateBootstrap(dr, snapshotID, layerBlobID, upperDirPath, &w)
			switch {
			case err != nil && !errdefs.IsAlreadyExists(err):
				return epilog(err, "generate tarfs from image layer blob")
			case err == nil && digester.Digest() != diffID:
				return epilog(err, "image layer diffID does not match")
			default:
				msg := fmt.Sprintf("Nydus tarfs for snapshot %s is ready, digest %s", snapshotID, digester.Digest())
				return epilog(nil, msg)
			}
		} else {
			err = t.generateBootstrap(ds, snapshotID, layerBlobID, upperDirPath, &w)
			if err != nil && !errdefs.IsAlreadyExists(err) {
				return epilog(err, "generate tarfs data from image layer blob")
			}
			msg := fmt.Sprintf("Nydus tarfs for snapshot %s is ready", snapshotID)
			return epilog(nil, msg)
		}
	}

	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return epilog(err, "create key chain for connection")
	}
	remote := remote.New(keyChain, t.insecure)
	rc, _, err := t.getBlobStream(ctx, remote, ref, layerDigest)
	if err != nil && remote.RetryWithPlainHTTP(ref, err) {
		rc, _, err = t.getBlobStream(ctx, remote, ref, layerDigest)
	}
	if err != nil {
		return epilog(err, "get blob stream for layer")
	}

	if retry {
		// Download and convert layer content in synchronous mode when retry for error recovering
		err = process(rc, remote)
	} else {
		// Download and convert layer content in background.
		// Will retry when the content is actually needed if the background process failed.
		go func() {
			_ = process(rc, remote)
		}()
	}

	return err
}

func (t *Manager) retryPrepareLayer(snapshotID, upperDirPath string, labels map[string]string) error {
	ref, ok := labels[label.CRIImageRef]
	if !ok {
		return errors.Errorf("not found image reference label")
	}
	layerDigest := digest.Digest(labels[label.CRILayerDigest])
	if err := layerDigest.Validate(); err != nil {
		return errors.Wrapf(err, "invalid layer digest")
	}
	manifestDigest := digest.Digest(labels[label.CRIManifestDigest])
	if err := manifestDigest.Validate(); err != nil {
		return errors.Wrapf(err, "invalid manifest digest")
	}

	st, err := t.getSnapshotStatusWithLock(snapshotID)
	if err != nil {
		return errors.Wrapf(err, "retry downloading content for snapshot %s", snapshotID)
	}
	ctx := st.ctx
	switch st.status {
	case TarfsStatusPrepare:
		log.L.Infof("Another thread is retrying snapshot %s, wait for the result", snapshotID)
		st.mutex.Unlock()
		_, err = t.waitLayerReady(snapshotID, false)
		return err
	case TarfsStatusReady:
		log.L.Infof("Another thread has retried snapshot %s and succeed", snapshotID)
		st.mutex.Unlock()
		return nil
	case TarfsStatusFailed:
		log.L.Infof("Snapshot %s is in FAILED state, retry downloading layer content", snapshotID)
		if st.wg == nil {
			st.wg = &sync.WaitGroup{}
			st.wg.Add(1)
		}
		st.status = TarfsStatusPrepare
		st.mutex.Unlock()
	}

	if err := t.blobProcess(ctx, snapshotID, ref, manifestDigest, layerDigest, upperDirPath, true); err != nil {
		log.L.WithError(err).Errorf("async prepare tarfs layer of snapshot ID %s", snapshotID)
	}

	return nil
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
		ctx:    ctx,
	}
	t.mutex.Unlock()

	return t.blobProcess(ctx, snapshotID, ref, manifestDigest, layerDigest, upperDirPath, false)
}

func (t *Manager) MergeLayers(ctx context.Context, s storage.Snapshot, storageLocater func(string) string,
	infoGetter func(ctx context.Context, id string) (string, snapshots.Info, error)) error {
	mergedBootstrap := t.imageMetaFilePath(storageLocater(s.ParentIDs[0]))
	if _, err := os.Stat(mergedBootstrap); err == nil {
		log.L.Debugf("tarfs snapshot %s already has merged bootstrap %s", s.ParentIDs[0], mergedBootstrap)
		return nil
	}

	bootstraps := []string{}
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		_, err := t.waitLayerReady(snapshotID, false)
		if err != nil {
			upperDir, info, err1 := infoGetter(ctx, snapshotID)
			if err1 == nil {
				err1 = t.retryPrepareLayer(snapshotID, upperDir, info.Labels)
				if err1 != nil {
					log.L.Errorf("failed to retry downloading content for snapshot %s, %s", snapshotID, err1)
				} else {
					err = nil
				}
			}
		}
		if err != nil {
			return errors.Wrapf(err, "wait for tarfs snapshot %s to get ready", snapshotID)
		}

		metaFilePath := t.layerMetaFilePath(storageLocater(snapshotID))
		bootstraps = append(bootstraps, metaFilePath)
	}

	// Merging image with only one layer is a noop, just copy the layer bootstrap as image bootstrap
	if len(s.ParentIDs) == 1 {
		metaFilePath := t.layerMetaFilePath(storageLocater(s.ParentIDs[0]))
		return errors.Wrapf(os.Link(metaFilePath, mergedBootstrap), "create hard link from image bootstrap to layer bootstrap")
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
	// Nothing to do for this case, all needed datum are ready.
	if !exportDisk && !withVerity {
		return updateFields, nil
	} else if !wholeImage != perLayer {
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
		diskFileName = t.layerDiskFilePath(blobID)
	}

	// Do not regenerate if the disk image already exists.
	if _, err := os.Stat(diskFileName); err == nil {
		return updateFields, nil
	}

	st, err := t.waitLayerReady(snapshotID, true)
	if err != nil {
		return updateFields, errors.Wrapf(err, "wait for tarfs snapshot %s to get ready", snapshotID)
	}
	defer st.mutex.Unlock()

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

func (t *Manager) MountErofs(snapshotID string, s *storage.Snapshot, labels map[string]string, rafs *rafs.Rafs) error {
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
	var parents []string
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		st, err := t.waitLayerReady(snapshotID, true)
		if err != nil {
			return errors.Wrapf(err, "wait for tarfs conversion task")
		}

		var blobMarker = "\"blob_id\":\"" + st.blobID + "\""
		if strings.Contains(blobInfo, blobMarker) {
			if st.dataLoopdev == nil {
				blobTarFilePath := t.layerTarFilePath(st.blobID)
				loopdev, err := t.attachLoopdev(blobTarFilePath)
				if err != nil {
					st.mutex.Unlock()
					return errors.Wrapf(err, "attach layer tar file %s to loopdev", blobTarFilePath)
				}
				st.dataLoopdev = loopdev
			}
			devices = append(devices, "device="+st.dataLoopdev.Name())
			parents = append(parents, snapshotID)
		}

		st.mutex.Unlock()
	}
	parentList := strings.Join(parents, ",")
	devices = append(devices, "ro")
	mountOpts := strings.Join(devices, ",")

	st, err := t.getSnapshotStatusWithLock(snapshotID)
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
	devName := st.metaLoopdev.Name()

	if err = os.MkdirAll(mountPoint, 0750); err != nil {
		return errors.Wrapf(err, "create tarfs mount dir %s", mountPoint)
	}

	err = unix.Mount(devName, mountPoint, "erofs", 0, mountOpts)
	if err != nil {
		return errors.Wrapf(err, "mount erofs at %s with opts %s", mountPoint, mountOpts)
	}
	st.erofsMountPoint = mountPoint
	rafs.SetMountpoint(mountPoint)
	rafs.AddAnnotation(label.NydusTarfsParents, parentList)
	return nil
}

func (t *Manager) RemountErofs(snapshotID string, rafs *rafs.Rafs) error {
	upperDirPath := path.Join(rafs.GetSnapshotDir(), "fs")

	log.L.Infof("remount EROFS for tarfs snapshot %s at %s", snapshotID, upperDirPath)
	var parents []string
	if parentList, ok := rafs.Annotations[label.NydusTarfsParents]; ok {
		parents = strings.Split(parentList, ",")
	} else {
		if !config.GetTarfsMountOnHost() {
			rafs.SetMountpoint(upperDirPath)
		}
		return nil
	}

	var devices []string
	for idx := 0; idx < len(parents); idx++ {
		snapshotID := parents[idx]
		st, err := t.waitLayerReady(snapshotID, true)
		if err != nil {
			return errors.Wrapf(err, "wait for tarfs conversion task")
		}

		if st.dataLoopdev == nil {
			blobTarFilePath := t.layerTarFilePath(st.blobID)
			loopdev, err := t.attachLoopdev(blobTarFilePath)
			if err != nil {
				st.mutex.Unlock()
				return errors.Wrapf(err, "attach layer tar file %s to loopdev", blobTarFilePath)
			}
			st.dataLoopdev = loopdev
		}
		devices = append(devices, "device="+st.dataLoopdev.Name())

		st.mutex.Unlock()
	}
	devices = append(devices, "ro")
	mountOpts := strings.Join(devices, ",")

	st, err := t.getSnapshotStatusWithLock(snapshotID)
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
		mergedBootstrap := t.imageMetaFilePath(upperDirPath)
		loopdev, err := t.attachLoopdev(mergedBootstrap)
		if err != nil {
			return errors.Wrapf(err, "attach merged bootstrap %s to loopdev", mergedBootstrap)
		}
		st.metaLoopdev = loopdev
	}
	devName := st.metaLoopdev.Name()

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
	st, err := t.getSnapshotStatusWithLock(snapshotID)
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
	st, err := t.getSnapshotStatusWithLock(snapshotID)
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
		err := deleteLoop(st.metaLoopdev)
		if err != nil {
			st.mutex.Unlock()
			return errors.Wrapf(err, "detach merged bootstrap loopdev for tarfs snapshot %s", snapshotID)
		}
		st.metaLoopdev = nil
	}

	if st.dataLoopdev != nil {
		err := deleteLoop(st.dataLoopdev)
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

func (t *Manager) RecoverSnapshoInfo(ctx context.Context, id string, info snapshots.Info, upperPath string) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	log.L.Infof("recover tarfs snapshot %s with path %s", id, upperPath)

	if _, ok := t.snapshotMap[id]; ok {
		// RecoverSnapshotInfo() is called after RecoverRafsInstance(), so there may be some snapshots already exist.
		return nil
	}

	layerMetaFilePath := t.layerMetaFilePath(upperPath)
	if _, err := os.Stat(layerMetaFilePath); err == nil {
		layerDigest := digest.Digest(info.Labels[label.CRILayerDigest])
		if err := layerDigest.Validate(); err != nil {
			return errors.Wrapf(err, "fetch layer digest label")
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.snapshotMap[id] = &snapshotStatus{
			status: TarfsStatusReady,
			blobID: layerDigest.Hex(),
			cancel: cancel,
			ctx:    ctx,
		}
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		wg := &sync.WaitGroup{}
		wg.Add(1)
		t.snapshotMap[id] = &snapshotStatus{
			status: TarfsStatusFailed,
			wg:     wg,
			cancel: cancel,
			ctx:    ctx,
		}
	}
	return nil
}

// This method is called in single threaded mode during startup, so we do not lock `snapshotStatus` objects.
func (t *Manager) RecoverRafsInstance(r *rafs.Rafs) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	log.L.Infof("recover rafs instance %s for tarfs", r.SnapshotID)

	if _, ok := t.snapshotMap[r.SnapshotID]; ok {
		return errors.Errorf("snapshot %s already exists", r.SnapshotID)
	}

	layerDigest := digest.Digest(r.Annotations[label.CRILayerDigest])
	if layerDigest.Validate() != nil {
		return errors.Errorf("invalid layer digest for snapshot %s", r.SnapshotID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	upperDir := path.Join(r.GetSnapshotDir(), "fs")
	metaFilePath := t.layerMetaFilePath(upperDir)

	if _, err := os.Stat(metaFilePath); err == nil {
		mountPoint := path.Join(r.GetSnapshotDir(), "mnt")
		mounted, err := mountinfo.Mounted(mountPoint)
		if !mounted || err != nil {
			mountPoint = ""
		}
		if !mounted && err == nil {
			if _, ok := r.Annotations[label.NydusTarfsParents]; ok {
				t.RemountMap[r.SnapshotID] = r
			}
		}
		t.snapshotMap[r.SnapshotID] = &snapshotStatus{
			status:          TarfsStatusReady,
			blobID:          layerDigest.Hex(),
			erofsMountPoint: mountPoint,
			cancel:          cancel,
			ctx:             ctx,
		}
	} else {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		t.snapshotMap[r.SnapshotID] = &snapshotStatus{
			status: TarfsStatusFailed,
			wg:     wg,
			cancel: cancel,
			ctx:    ctx,
		}
	}

	return nil
}

func (t *Manager) getSnapshotStatusWithLock(snapshotID string) (*snapshotStatus, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[snapshotID]
	if ok {
		st.mutex.Lock()
		return st, nil
	}
	return nil, errors.Errorf("not found snapshot %s", snapshotID)
}

func (t *Manager) waitLayerReady(snapshotID string, lock bool) (*snapshotStatus, error) {
	st, err := t.getSnapshotStatusWithLock(snapshotID)
	if err != nil {
		return nil, err
	}

	if st.wg != nil && st.status == TarfsStatusPrepare {
		wg := st.wg
		st.mutex.Unlock()
		log.L.Debugf("wait tarfs conversion task for snapshot %s", snapshotID)
		wg.Wait()
		st, err = t.getSnapshotStatusWithLock(snapshotID)
		if err != nil {
			return nil, err
		}
	}

	if st.status != TarfsStatusReady {
		state := tarfsStatusString(st.status)
		st.mutex.Unlock()
		return nil, errors.Errorf("snapshot %s is in %s state instead of Ready", snapshotID, state)
	}

	if !lock {
		st.mutex.Unlock()
	}
	return st, nil
}

func (t *Manager) attachLoopdev(blob string) (*os.File, error) {
	// losetup.Attach() is not thread-safe hold lock here
	t.mutexLoopDev.Lock()
	defer t.mutexLoopDev.Unlock()
	param := LoopParams{
		Readonly:  true,
		Autoclear: true,
	}
	return setupLoop(blob, param)
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
		label.CRIImageRef,
		label.CRILayerDigest,
		label.CRIManifestDigest,
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

func (t *Manager) layerDiskFilePath(blobID string) string {
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

func tarfsStatusString(status int) string {
	switch status {
	case TarfsStatusReady:
		return "Ready"
	case TarfsStatusPrepare:
		return "Prepare"
	case TarfsStatusFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}
