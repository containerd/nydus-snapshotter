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
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
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
	snapshotMap    map[string]SnapshotStatus // tarfs snapshots status, indexed by snapshot ID
	mutex          sync.Mutex
	IsAsyncFormat  bool // format uncompressed blob to tarfile and generate bootstrap asynchronously
	insecure       bool
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

type SnapshotStatus struct {
	snapshotStatus  int
	layerBsLoopdev  *losetup.Device
	mergedBsLoopdev *losetup.Device
	erofsMountPoint string
	wg              *sync.WaitGroup
	cancle          context.CancelFunc
}

func NewManager(insecure, asyncFormat bool, nydusImagePath string) *Manager {
	return &Manager{
		snapshotMap:    map[string]SnapshotStatus{},
		IsAsyncFormat:  asyncFormat,
		nydusImagePath: nydusImagePath,
		insecure:       insecure,
		diffIDCache:    lru.New(1000),
		sg:             singleflight.Group{},
	}
}

// fetch and cache blob digests and diff ids of an oci image
func (t *Manager) fetchImageDiffID(ctx context.Context, remote *remote.Remote, ref, manifest string) error {
	// fetch the manifest find config digest and layers digest
	rc, err := t.getBlobStream(ctx, remote, ref, manifest)
	if err != nil {
		return err
	}
	var manifestOCI ocispec.Manifest
	bytes, err := io.ReadAll(rc)
	rc.Close()
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
	rc, err = t.getBlobStream(ctx, remote, ref, string(manifestOCI.Config.Digest))
	if err != nil {
		return errors.Wrap(err, "fetch referrers")
	}
	var config ocispec.Image
	bytes, err = io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return errors.Wrap(err, "read config")
	}
	if err := json.Unmarshal(bytes, &config); err != nil {
		return errors.Wrap(err, "unmarshal config")
	}
	if len(config.RootFS.DiffIDs) != len(manifestOCI.Layers) {
		return errors.Errorf("number of diff ids unmatch manifest layers")
	}

	// cache OCI blob digest & diff id
	for i := range manifestOCI.Layers {
		t.diffIDCache.Add(string(manifestOCI.Layers[i].Digest), string(config.RootFS.DiffIDs[i]))
	}
	return nil
}

func (t *Manager) getBlobDiffID(ctx context.Context, remote *remote.Remote, ref, manifest, layerDigest string) (string, error) {
	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(string), nil
	}

	if _, err, _ := t.sg.Do(ref, func() (interface{}, error) {
		err := t.fetchImageDiffID(ctx, remote, ref, manifest)
		return nil, err
	}); err != nil {
		return "", err
	}

	if diffid, ok := t.diffIDCache.Get(layerDigest); ok {
		return diffid.(string), nil
	}

	return "", errors.Errorf("get blob diff id failed")
}

func (t *Manager) getBlobStream(ctx context.Context, remote *remote.Remote, ref, layerDigest string) (io.ReadCloser, error) {
	fetcher, err := remote.Fetcher(ctx, ref)
	if err != nil {
		return nil, errors.Wrap(err, "get remote fetcher")
	}

	fetcherByDigest, ok := fetcher.(remotes.FetcherByDigest)
	if !ok {
		return nil, errors.Errorf("fetcher %T does not implement remotes.FetcherByDigest", fetcher)
	}

	dgst, err := digest.Parse(layerDigest)
	if err != nil {
		return nil, errors.Wrap(err, "layerDigest format")
	}

	rc, _, err := fetcherByDigest.FetchByDigest(ctx, dgst)
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
	err = syscall.Mkfifo(fifoName, 0644)
	if err != nil {
		return false, err
	}
	defer os.Remove(fifoName)

	go func() {
		fifoFile, err := os.OpenFile(fifoName, os.O_WRONLY, os.ModeNamedPipe)
		if err != nil {
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
	// losetup.Attach() is not thread-safe hold lock here if we attach loop async
	if t.IsAsyncFormat {
		t.mutex.Lock()
		defer t.mutex.Unlock()
	}
	dev, err := losetup.Attach(blob, 0, false)
	return &dev, err
}

// uncompress oci blob, generate tarfs bootstrap
func (t *Manager) prepareLayer(ctx context.Context, snapshotID, ref, manifest, layerDigest, storagePath string) (*losetup.Device, error) {
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
		if digester.Digest() != digest.Digest(diffID) {
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

	loop, err := t.attachLoopdev(filepath.Join(storagePath, "layer_"+snapshotID+"_"+TarfsBlobName))
	return loop, err
}

func (t *Manager) PrepareTarfsLayer(snapshotID, ref, manifest, layerDigest, storagePath string) error {
	t.mutex.Lock()
	if _, ok := t.snapshotMap[snapshotID]; ok {
		t.mutex.Unlock()
		return errors.Errorf("snapshot %s already prapared", snapshotID)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	defer wg.Done()
	ctx, cancel := context.WithCancel(context.Background())

	t.snapshotMap[snapshotID] = SnapshotStatus{
		snapshotStatus: TarfsStatusFormatting,
		wg:             wg,
		cancle:         cancel,
	}
	t.mutex.Unlock()

	loopdev, err := t.prepareLayer(ctx, snapshotID, ref, manifest, layerDigest, storagePath)

	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[snapshotID]
	if !ok {
		// snapshot can be deleted during async prepare
		return errors.Errorf("can not found snapshot status after prepare")
	}
	if err != nil {
		if errors.Is(err, ErrEmptyBlob) {
			st.snapshotStatus = TarfsStatusReady
			err = nil
		} else {
			st.snapshotStatus = TarfsStatusFailed
		}
	} else {
		st.snapshotStatus = TarfsStatusReady
		st.layerBsLoopdev = loopdev
	}
	t.snapshotMap[snapshotID] = st
	return err
}

func (t *Manager) MergeTarfsLayers(s storage.Snapshot, storageLocater func(string) string) error {
	mergedBootstrap := filepath.Join(storageLocater(s.ParentIDs[0]), TarfsMeragedBootstapName)
	if _, err := os.Stat(mergedBootstrap); err == nil {
		log.L.Infof("tarfs snapshot %s already has merged bootstrap %s", s.ParentIDs[0], mergedBootstrap)
		return nil
	}

	bootstraps := []string{}
	// When merging bootstrap, we need to arrange layer bootstrap in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		if t.IsAsyncFormat {
			err := t.waitLayerFormating(snapshotID)
			if err != nil {
				return errors.Wrapf(err, "wait layer formating err")
			}
		}

		st, err := t.GetSnapshotStatus(snapshotID)
		if err != nil {
			return err
		}
		if st.snapshotStatus != TarfsStatusReady {
			return errors.Errorf("snapshot %s tarfs format error %d", snapshotID, st.snapshotStatus)
		}
		bootstraps = append(bootstraps, filepath.Join(storageLocater(snapshotID), TarfsLayerBootstapName))
	}

	options := []string{
		"merge",
		"--bootstrap", mergedBootstrap,
	}
	options = append(options, bootstraps...)
	cmd := exec.Command(t.nydusImagePath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.L.Infof("nydus image command %v", options)
	err := cmd.Run()
	if err != nil {
		return errors.Wrap(err, "merging tarfs layers")
	}

	loopdev, err := t.attachLoopdev(mergedBootstrap)
	if err != nil {
		return errors.Wrap(err, "attach bootstrap to loop error")
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[s.ParentIDs[0]]
	if !ok {
		return errors.Errorf("snapshot %s not found", s.ParentIDs[0])
	}
	st.mergedBsLoopdev = loopdev
	t.snapshotMap[s.ParentIDs[0]] = st

	return nil
}

func (t *Manager) MountTarErofs(s storage.Snapshot, mountPoint string) error {
	var opts string
	var devName string

	st, err := t.GetSnapshotStatus(s.ParentIDs[0])
	if err != nil {
		return err
	}
	if st.erofsMountPoint == mountPoint {
		log.L.Infof("erofs tarfs %s already mounted", mountPoint)
		return nil
	}

	if err = os.MkdirAll(mountPoint, 0755); err != nil {
		return errors.Wrapf(err, "failed to create tarfs mount dir %s", mountPoint)
	}

	// mount opt for multi devices, we need to arrange layer loopdev in order from low to high
	for idx := len(s.ParentIDs) - 1; idx >= 0; idx-- {
		snapshotID := s.ParentIDs[idx]
		st, err := t.GetSnapshotStatus(snapshotID)
		if err != nil {
			return err
		}

		if st.snapshotStatus != TarfsStatusReady {
			return errors.Errorf("snapshot %s tarfs format error %d", snapshotID, st.snapshotStatus)
		}
		if idx == 0 {
			if st.mergedBsLoopdev != nil {
				devName = st.mergedBsLoopdev.Path()
			} else {
				return errors.Errorf("snapshot %s not found boostrap loopdev error %d", snapshotID, st.snapshotStatus)
			}
		}
		// skip empty blob
		if st.layerBsLoopdev != nil {
			opts += "device=" + st.layerBsLoopdev.Path() + ","
		}
	}

	err = unix.Mount(devName, mountPoint, "erofs", 0, opts)
	if err != nil {
		return errors.Errorf("mount erofs at %s with opts %s err %v", mountPoint, opts, err)
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	// this is safe during a snapshot prepare the parent snapshot can not be removed
	st.erofsMountPoint = mountPoint
	t.snapshotMap[s.ParentIDs[0]] = st
	return nil
}

func (t *Manager) waitLayerFormating(snapshotID string) error {
	log.L.Debugf("wait for tarfs formating snapshot %s", snapshotID)
	st, err := t.GetSnapshotStatus(snapshotID)
	if err != nil {
		return err
	}
	st.wg.Wait()
	return nil
}

// `snapshotID` refers to the tarfs merged bootstrap snapshot
func (t *Manager) RemoteMountTarfs(upperdir, workdir, snapshotID string) ([]mount.Mount, error) {
	var overlayOptions []string

	ok, erofsMp := t.IsMeragedTarfsLayer(snapshotID)
	if !ok {
		return nil, errors.Errorf("bootstrap snapshot %s status error", snapshotID)
	}

	overlayOptions = append(overlayOptions,
		"workdir="+workdir,
		"upperdir="+upperdir,
		"lowerdir="+erofsMp,
	)

	return []mount.Mount{
		{
			Type:    "overlay",
			Source:  "overlay",
			Options: overlayOptions,
		},
	}, nil
}

// check if a snapshot mounted a erofs tarfs
func (t *Manager) IsMeragedTarfsLayer(snapshotID string) (bool, string) {
	st, err := t.GetSnapshotStatus(snapshotID)
	if err != nil || len(st.erofsMountPoint) == 0 {
		return false, ""
	}
	return true, st.erofsMountPoint
}

func (t *Manager) DetachTarfsLayer(snapshotID string) error {
	st, err := t.GetSnapshotStatus(snapshotID)
	if err != nil {
		return errors.Wrapf(err, "detach a tarfs snapshot %s which is already removed", snapshotID)
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
	st.cancle()

	t.mutex.Lock()
	defer t.mutex.Unlock()
	delete(t.snapshotMap, snapshotID)
	return nil
}

func (t *Manager) GetSnapshotStatus(snapshotID string) (SnapshotStatus, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	st, ok := t.snapshotMap[snapshotID]
	if ok {
		return st, nil
	}
	return SnapshotStatus{}, errors.Errorf("not found snapshot %s", snapshotID)
}
