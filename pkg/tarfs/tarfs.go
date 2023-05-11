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
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/remote"
	"github.com/containerd/nydus-snapshotter/pkg/remote/remotes"
	losetup "github.com/freddierice/go-losetup"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/unix"
	"k8s.io/utils/lru"
)

type Manager struct {
	snapshotMap    map[string]*snapshotStatus // tarfs snapshots status, indexed by snapshot ID
	mutex          sync.Mutex
	insecure       bool
	checkTarfsHint bool       // whether to rely on tarfs hint annotation
	tarfsHintCache *lru.Cache // cache oci image raf and tarfs hint annotation
	nydusImagePath string
	diffIDCache    *lru.Cache // cache oci blob digest and diffID
	sg             singleflight.Group
}

const (
	TarfsStatusInit       = 0
	TarfsStatusFormatting = 1
	TarfsStatusReady      = 2
	TarfsStatusFailed     = 3
)

const (
	TarfsBlobName            = "blob.tar"
	TarfsLayerBootstapName   = "layer_bootstrap"
	TarfsMeragedBootstapName = "merged_bootstrap"
)

var ErrEmptyBlob = errors.New("empty blob")

type snapshotStatus struct {
	status          int
	layerBsLoopdev  *losetup.Device
	mergedBsLoopdev *losetup.Device
	erofsMountPoint string
	erofsMountOpts  string
	wg              *sync.WaitGroup
	cancel          context.CancelFunc
}

func NewManager(insecure, checkTarfsHint bool, nydusImagePath string) *Manager {
	return &Manager{
		snapshotMap:    map[string]*snapshotStatus{},
		nydusImagePath: nydusImagePath,
		insecure:       insecure,
		checkTarfsHint: checkTarfsHint,
		tarfsHintCache: lru.New(50),
		diffIDCache:    lru.New(1000),
		sg:             singleflight.Group{},
	}
}

// fetch image form manifest and config, then cache them in lru
// FIXME need an update policy
func (t *Manager) fetchImageInfo(ctx context.Context, remote *remote.Remote, ref string, manifest digest.Digest) error {
	// fetch the manifest find config digest and layers digest
	rc, err := t.getBlobStream(ctx, remote, ref, manifest)
	if err != nil {
		return err
	}
	defer rc.Close()
	var manifestOCI ocispec.Manifest
	bytes, err := io.ReadAll(rc)
	if err != nil {
		return errors.Wrap(err, "read manifest")
	}
	if err := json.Unmarshal(bytes, &manifestOCI); err != nil {
		return errors.Wrap(err, "unmarshal manifest")
	}
	if len(manifestOCI.Layers) < 1 {
		return errors.Errorf("invalid manifest")
	}

	// fetch the config and find diff ids
	rc, err = t.getBlobStream(ctx, remote, ref, manifestOCI.Config.Digest)
	if err != nil {
		return errors.Wrap(err, "fetch referrers")
	}
	defer rc.Close()
	var config ocispec.Image
	bytes, err = io.ReadAll(rc)
	if err != nil {
		return errors.Wrap(err, "read config")
	}
	if err := json.Unmarshal(bytes, &config); err != nil {
		return errors.Wrap(err, "unmarshal config")
	}
	if len(config.RootFS.DiffIDs) != len(manifestOCI.Layers) {
		return errors.Errorf("number of diff ids unmatch manifest layers")
	}
	// cache ref & tarfs hint annotation
	t.tarfsHintCache.Add(ref, label.IsTarfsHint(manifestOCI.Annotations))
	// cache OCI blob digest & diff id
	for i := range manifestOCI.Layers {
		t.diffIDCache.Add(manifestOCI.Layers[i].Digest, config.RootFS.DiffIDs[i])
	}
	return nil
}

func (t *Manager) getBlobDiffID(ctx context.Context, remote *remote.Remote, ref string, manifest, layerDigest digest.Digest) (digest.Digest, error) {
	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(digest.Digest), nil
	}

	if _, err, _ := t.sg.Do(ref, func() (interface{}, error) {
		err := t.fetchImageInfo(ctx, remote, ref, manifest)
		return nil, err
	}); err != nil {
		return "", err
	}

	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(digest.Digest), nil
	}

	return "", errors.Errorf("get blob diff id failed")
}

func (t *Manager) getBlobStream(ctx context.Context, remote *remote.Remote, ref string, layerDigest digest.Digest) (io.ReadCloser, error) {
	fetcher, err := remote.Fetcher(ctx, ref)
	if err != nil {
		return nil, errors.Wrap(err, "get remote fetcher")
	}

	fetcherByDigest, ok := fetcher.(remotes.FetcherByDigest)
	if !ok {
		return nil, errors.Errorf("fetcher %T does not implement remotes.FetcherByDigest", fetcher)
	}

	rc, _, err := fetcherByDigest.FetchByDigest(ctx, layerDigest)
	return rc, err
}

// generate tar file and layer bootstrap, return if this blob is an empty blob
func (t *Manager) generateBootstrap(tarReader io.Reader, storagePath, snapshotID string) (emptyBlob bool, err error) {
	layerBootstrap := filepath.Join(storagePath, TarfsLayerBootstapName)
	blob := filepath.Join(storagePath, "layer_"+snapshotID+"_"+TarfsBlobName)

	tarFile, err := os.Create(blob)
	if err != nil {
		return false, err
	}
	defer tarFile.Close()

	fifoName := filepath.Join(storagePath, "layer_"+snapshotID+"_"+"tar.fifo")
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
		"--bootstrap", layerBootstrap,
		"--blob-id", "layer_" + snapshotID + "_" + TarfsBlobName,
		"--blob-dir", storagePath,
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
		return false, errors.Wrap(err, "converting tarfs layer failed")
	}
	log.L.Debugf("nydus image output %s", outb.String())
	log.L.Debugf("nydus image err %s", errb.String())

	// TODO need a more reliable way to check if this is an empty blob
	if strings.Contains(outb.String(), "data blob size: 0x0") ||
		strings.Contains(errb.String(), "data blob size: 0x0") {
		return true, nil
	}
	return false, nil
}

func (t *Manager) attachLoopdev(blob string) (*losetup.Device, error) {
	// losetup.Attach() is not thread-safe hold lock here
	t.mutex.Lock()
	defer t.mutex.Unlock()
	dev, err := losetup.Attach(blob, 0, false)
	return &dev, err
}

// download & uncompress oci blob, generate tarfs bootstrap
func (t *Manager) blobProcess(ctx context.Context, snapshotID, ref string, manifest, layerDigest digest.Digest, storagePath string) (*losetup.Device, error) {
	keyChain, err := auth.GetKeyChainByRef(ref, nil)
	if err != nil {
		return nil, err
	}
	remote := remote.New(keyChain, t.insecure)

	handle := func() (bool, error) {
		diffID, err := t.getBlobDiffID(ctx, remote, ref, manifest, layerDigest)
		if err != nil {
			return false, err
		}

		rc, err := t.getBlobStream(ctx, remote, ref, layerDigest)
		if err != nil {
			return false, err
		}
		defer rc.Close()

		ds, err := compression.DecompressStream(rc)
		if err != nil {
			return false, errors.Wrap(err, "unpack stream")
		}
		defer ds.Close()

		digester := digest.Canonical.Digester()
		dr := io.TeeReader(ds, digester.Hash())

		emptyBlob, err := t.generateBootstrap(dr, storagePath, snapshotID)
		if err != nil {
			return false, err
		}
		log.L.Infof("prepare tarfs Layer generateBootstrap done layer %s, digest %s", snapshotID, digester.Digest())
		if digester.Digest() != diffID {
			return false, errors.Errorf("diff id %s not match", diffID)
		}
		return emptyBlob, nil
	}

	var emptyBlob bool
	emptyBlob, err = handle()
	if err != nil && remote.RetryWithPlainHTTP(ref, err) {
		emptyBlob, err = handle()
	}
	if err != nil {
		return nil, err
	}

	// for empty blob do not need a loop device
	if emptyBlob {
		return nil, ErrEmptyBlob
	}

	return t.attachLoopdev(filepath.Join(storagePath, "layer_"+snapshotID+"_"+TarfsBlobName))
}

func (t *Manager) PrepareLayer(snapshotID, ref string, manifest, layerDigest digest.Digest, storagePath string) error {
	t.mutex.Lock()
	if _, ok := t.snapshotMap[snapshotID]; ok {
		t.mutex.Unlock()
		return errors.Errorf("snapshot %s already prapared", snapshotID)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	defer wg.Done()
	ctx, cancel := context.WithCancel(context.Background())

	t.snapshotMap[snapshotID] = &snapshotStatus{
		status: TarfsStatusFormatting,
		wg:     wg,
		cancel: cancel,
	}
	t.mutex.Unlock()

	loopdev, err := t.blobProcess(ctx, snapshotID, ref, manifest, layerDigest, storagePath)

	st, err1 := t.getSnapshotStatus(snapshotID)
	if err1 != nil {
		return errors.Errorf("can not found snapshot status after prepare")
	}
	if err != nil {
		if errors.Is(err, ErrEmptyBlob) {
			st.status = TarfsStatusReady
			err = nil
		} else {
			st.status = TarfsStatusFailed
		}
	} else {
		st.status = TarfsStatusReady
		st.layerBsLoopdev = loopdev
	}
	return err
}

func (t *Manager) MergeLayers(s storage.Snapshot, storageLocater func(string) string) error {
	mergedBootstrap := filepath.Join(storageLocater(s.ParentIDs[0]), TarfsMeragedBootstapName)
	if _, err := os.Stat(mergedBootstrap); err == nil {
		log.L.Infof("tarfs snapshot %s already has merged bootstrap %s", s.ParentIDs[0], mergedBootstrap)
		return nil
	}

	var mountOpts string
	bootstraps := []string{}
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		err := t.waitLayerFormating(snapshotID)
		if err != nil {
			return errors.Wrapf(err, "wait layer formating err")
		}

		st, err := t.getSnapshotStatus(snapshotID)
		if err != nil {
			return err
		}
		if st.status != TarfsStatusReady {
			return errors.Errorf("snapshot %s tarfs format error %d", snapshotID, st.status)
		}
		bootstraps = append(bootstraps, filepath.Join(storageLocater(snapshotID), TarfsLayerBootstapName))

		// mount opt skip empty blob
		if st.layerBsLoopdev != nil {
			mountOpts += "device=" + st.layerBsLoopdev.Path() + ","
		}
	}

	options := []string{
		"merge",
		"--bootstrap", mergedBootstrap,
	}
	options = append(options, bootstraps...)
	cmd := exec.Command(t.nydusImagePath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.L.Debugf("nydus image command %v", options)
	err := cmd.Run()
	if err != nil {
		return errors.Wrap(err, "merging tarfs layers")
	}

	loopdev, err := t.attachLoopdev(mergedBootstrap)
	if err != nil {
		return errors.Wrap(err, "attach bootstrap to loop error")
	}

	st, err := t.getSnapshotStatus(s.ParentIDs[0])
	if err != nil {
		return errors.Errorf("snapshot %s not found", s.ParentIDs[0])
	}
	st.mergedBsLoopdev = loopdev
	st.erofsMountOpts = mountOpts

	return nil
}

func (t *Manager) MountTarErofs(snapshotID string, mountPoint string) error {
	var devName string

	st, err := t.getSnapshotStatus(snapshotID)
	if err != nil {
		return err
	}
	if len(st.erofsMountPoint) > 0 {
		if st.erofsMountPoint == mountPoint {
			log.L.Debugf("erofs tarfs %s already mounted", mountPoint)
			return nil
		}
		return errors.Errorf("erofs snapshot %s already mounted at %s", snapshotID, st.erofsMountPoint)
	}

	if err = os.MkdirAll(mountPoint, 0755); err != nil {
		return errors.Wrapf(err, "failed to create tarfs mount dir %s", mountPoint)
	}

	if st.mergedBsLoopdev != nil {
		devName = st.mergedBsLoopdev.Path()
	} else {
		return errors.Errorf("snapshot %s not found boostrap loopdev error %d", snapshotID, st.status)
	}

	err = unix.Mount(devName, mountPoint, "erofs", 0, st.erofsMountOpts)
	if err != nil {
		return errors.Wrapf(err, "mount erofs at %s with opts %s", mountPoint, st.erofsMountOpts)
	}
	st.erofsMountPoint = mountPoint
	return nil
}

func (t *Manager) UmountTarErofs(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID)
	if err != nil {
		return errors.Wrapf(err, "umount a tarfs snapshot %s which is already removed", snapshotID)
	}

	if len(st.erofsMountPoint) > 0 {
		err := unix.Unmount(st.erofsMountPoint, 0)
		if err != nil {
			return errors.Wrapf(err, "umount erofs tarfs %s failed", st.erofsMountPoint)
		}
	}
	st.erofsMountPoint = ""
	return nil
}

func (t *Manager) waitLayerFormating(snapshotID string) error {
	log.L.Debugf("wait for tarfs formating snapshot %s", snapshotID)
	st, err := t.getSnapshotStatus(snapshotID)
	if err != nil {
		return err
	}
	st.wg.Wait()
	return nil
}

// check if a snapshot is tarfs layer and if mounted a erofs tarfs
func (t *Manager) CheckTarfsLayer(snapshotID string, merged bool) bool {
	st, err := t.getSnapshotStatus(snapshotID)
	if err != nil {
		return false
	}
	if merged && len(st.erofsMountPoint) == 0 {
		return false
	}
	return true
}

func (t *Manager) DetachLayer(snapshotID string) error {
	st, err := t.getSnapshotStatus(snapshotID)
	if err != nil {
		return os.ErrNotExist
	}

	if len(st.erofsMountPoint) > 0 {
		err := unix.Unmount(st.erofsMountPoint, 0)
		if err != nil {
			return errors.Wrapf(err, "umount erofs tarfs %s failed", st.erofsMountPoint)
		}
	}

	if st.mergedBsLoopdev != nil {
		err := st.mergedBsLoopdev.Detach()
		if err != nil {
			return errors.Wrapf(err, "detach merged bootstrap loopdev for tarfs snapshot %s failed", snapshotID)
		}
	}

	if st.layerBsLoopdev != nil {
		err := st.layerBsLoopdev.Detach()
		if err != nil {
			return errors.Wrapf(err, "detach layer bootstrap loopdev for tarfs snapshot %s failed", snapshotID)
		}
	}
	st.cancel()

	t.mutex.Lock()
	defer t.mutex.Unlock()
	delete(t.snapshotMap, snapshotID)
	return nil
}

func (t *Manager) getSnapshotStatus(snapshotID string) (*snapshotStatus, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[snapshotID]
	if ok {
		return st, nil
	}
	return nil, errors.Errorf("not found snapshot %s", snapshotID)
}

func (t *Manager) CheckTarfsHintAnnotation(ctx context.Context, ref string, manifest digest.Digest) (bool, error) {
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
			err := t.fetchImageInfo(ctx, remote, ref, manifest)
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
		return handle()
	}
	return tarfsHint, err
}
