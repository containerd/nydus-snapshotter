/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package filesystem

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/KarpelesLab/reflink"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/log"
	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func (fs *Filesystem) UpperPath(id string) string {
	return filepath.Join(config.GetSnapshotsRootDir(), id, "fs")
}

func (fs *Filesystem) StargzEnabled() bool {
	return fs.stargzResolver != nil
}

// Detect if the blob is type of estargz by downloading its footer since estargz image does not
// have any characteristic annotation.
func (fs *Filesystem) IsStargzDataLayer(labels map[string]string) (bool, *stargz.Blob) {

	ref, layerDigest := registry.ParseLabels(labels)
	if ref == "" || layerDigest == "" {
		return false, nil
	}

	log.L.Infof("Checking stargz image ref %s digest %s", ref, layerDigest)

	keychain, err := auth.GetKeyChainByRef(ref, labels)
	if err != nil {
		log.L.WithError(err).Warn("get keychain from image reference")
		return false, nil
	}
	blob, err := fs.stargzResolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		log.L.WithError(err).Warn("get stargz blob")
		return false, nil
	}
	off, err := blob.GetTocOffset()
	if err != nil {
		log.L.WithError(err).Warn("get toc offset")
		return false, nil
	}
	if off <= 0 {
		log.L.WithError(err).Warnf("Invalid stargz toc offset %d", off)
		return false, nil
	}

	return true, blob
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
		cmd := exec.Command(fs.nydusdBinaryPath, options...)
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
		if err := os.Chmod(mergedBootstrap, 0440); err != nil {
			return err
		}
	}

	return nil
}

// Generate nydus bootstrap from stargz layers
// Download estargz TOC part from each layer as `nydus-image` conversion source.
// After conversion, a nydus metadata or bootstrap is used to pointing to each estargz blob
func (fs *Filesystem) PrepareStargzMetaLayer(blob *stargz.Blob, storagePath string, _ map[string]string) error {
	ref := blob.GetImageReference()
	layerDigest := blob.GetDigest()

	if !fs.StargzEnabled() {
		return fmt.Errorf("stargz compatibility is not enabled")
	}

	blobID := digest.Digest(layerDigest).Hex()
	convertedBootstrap := filepath.Join(storagePath, blobID)
	stargzFile := filepath.Join(storagePath, stargz.TocFileName)
	if _, err := os.Stat(convertedBootstrap); err == nil {
		return nil
	}

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		log.L.Infof("total stargz prepare layer duration %d", duration.Milliseconds())
	}()

	r, err := blob.ReadToc()
	if err != nil {
		return errors.Wrapf(err, "read TOC, image reference: %s, layer digest: %s", ref, layerDigest)
	}
	starGzToc, err := os.OpenFile(stargzFile, os.O_CREATE|os.O_RDWR, 0640)
	if err != nil {
		return errors.Wrap(err, "create stargz index")
	}

	defer starGzToc.Close()

	_, err = io.Copy(starGzToc, r)
	if err != nil {
		return errors.Wrap(err, "save stargz index")
	}
	err = os.Chmod(stargzFile, 0440)
	if err != nil {
		return err
	}

	blobMetaPath := filepath.Join(fs.cacheMgr.CacheDir(), fmt.Sprintf("%s.blob.meta", blobID))
	if config.GetFsDriver() == config.FsDriverFscache {
		// For fscache, the cache directory is managed linux fscache driver, so the blob.meta file
		// can't be stored there.
		if err := os.MkdirAll(storagePath, 0750); err != nil {
			return errors.Wrapf(err, "failed to create fscache work dir %s", storagePath)
		}
		blobMetaPath = filepath.Join(storagePath, fmt.Sprintf("%s.blob.meta", blobID))
	}

	tf, err := os.CreateTemp(storagePath, "converting-stargz")
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
	options = append(options, filepath.Join(storagePath, stargz.TocFileName))
	cmd := exec.Command(fs.nydusdBinaryPath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	log.L.Infof("nydus image command %v", options)
	err = cmd.Run()
	if err != nil {
		return errors.Wrap(err, "converting stargz layer")
	}

	err = os.Rename(tf.Name(), convertedBootstrap)
	if err != nil {
		return errors.Wrap(err, "rename converted stargz layer")
	}

	if err := os.Chmod(convertedBootstrap, 0440); err != nil {
		return err
	}

	return nil
}

func (fs *Filesystem) StargzLayer(labels map[string]string) bool {
	return labels[label.StargzLayer] != ""
}
