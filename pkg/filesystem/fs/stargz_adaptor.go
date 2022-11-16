/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package fs

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
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/internal/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/stargz"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

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
		log.L.WithError(err).Warn("get key chain by ref")
		return false, ref, layerDigest, nil
	}
	blob, err := fs.stargzResolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		log.L.WithError(err).Warn("get stargz blob")
		return false, ref, layerDigest, nil
	}
	off, err := blob.GetTocOffset()
	if err != nil {
		log.L.WithError(err).Warn("get toc offset")
		return false, ref, layerDigest, nil
	}
	if off <= 0 {
		log.L.WithError(err).Warnf("invalid stargz toc offset %d", off)
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
