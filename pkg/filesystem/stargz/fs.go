/*
 * Copyright (c) 2020. Ant Group. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package stargz

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/containerd/nydus-snapshotter/config"
	"github.com/containerd/nydus-snapshotter/pkg/auth"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/filesystem/meta"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/containerd/nydus-snapshotter/pkg/process"
	"github.com/containerd/nydus-snapshotter/pkg/utils/registry"
)

type Filesystem struct {
	meta.FileSystemMeta
	manager               *process.Manager
	daemonCfg             config.DaemonConfig
	resolver              *Resolver
	vpcRegistry           bool
	nydusdBinaryPath      string
	nydusdImageBinaryPath string
	logLevel              string
	logDir                string
	logToStdout           bool
	nydusdThreadNum       int
}

func NewFileSystem(ctx context.Context, opt ...NewFSOpt) (*Filesystem, error) {
	var fs Filesystem
	for _, o := range opt {
		err := o(&fs)
		if err != nil {
			return nil, err
		}
	}
	fs.resolver = NewResolver()

	return &fs, nil
}

func (f *Filesystem) CleanupBlobLayer(ctx context.Context, key string, async bool) error {
	return nil
}

func (f *Filesystem) PrepareBlobLayer(ctx context.Context, snapshot storage.Snapshot, labels map[string]string) error {
	return nil
}

func (f *Filesystem) PrepareMetaLayer(ctx context.Context, s storage.Snapshot, labels map[string]string) error {
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
	blob, err := f.resolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		return errors.Wrapf(err, "failed to get blob from ref %s, digest %s", ref, layerDigest)
	}
	r, err := blob.ReadToc()
	if err != nil {
		return errors.Wrapf(err, "failed to read toc from ref %s, digest %s", ref, layerDigest)
	}
	starGzToc, err := os.OpenFile(filepath.Join(f.UpperPath(s.ID), stargzToc), os.O_CREATE|os.O_RDWR, 0755)
	if err != nil {
		return errors.Wrap(err, "failed to create stargz index")
	}
	_, err = io.Copy(starGzToc, r)
	if err != nil {
		return errors.Wrap(err, "failed to save stargz index")
	}
	options := []string{
		"create",
		"--source-type", "stargz_index",
		"--bootstrap", filepath.Join(f.UpperPath(s.ID), "image.boot"),
		"--blob-id", digest(layerDigest).Sha256(),
		"--repeatable",
		"--disable-check",
	}
	if getParentSnapshotID(s) != "" {
		parentBootstrap := filepath.Join(f.UpperPath(getParentSnapshotID(s)), "image.boot")
		if _, err := os.Stat(parentBootstrap); err != nil {
			return fmt.Errorf("failed to find parentBootstrap from %s", parentBootstrap)
		}
		options = append(options,
			"--parent-bootstrap", parentBootstrap)
	}
	options = append(options, filepath.Join(f.UpperPath(s.ID), stargzToc))
	log.G(ctx).Infof("nydus image command %v", options)
	cmd := exec.Command(f.nydusdImageBinaryPath, options...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func getParentSnapshotID(s storage.Snapshot) string {
	if len(s.ParentIDs) == 0 {
		return ""
	}
	return s.ParentIDs[0]
}

func (f *Filesystem) Support(ctx context.Context, labels map[string]string) bool {
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
	blob, err := f.resolver.GetBlob(ref, layerDigest, keychain)
	if err != nil {
		logrus.WithError(err).Warn("get stargz blob")
		return false
	}
	off, err := blob.getTocOffset()
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

func (f *Filesystem) createNewDaemon(snapshotID string, imageID string) (*daemon.Daemon, error) {
	d, err := daemon.NewDaemon(
		daemon.WithSnapshotID(snapshotID),
		daemon.WithSocketDir(f.SocketRoot()),
		daemon.WithConfigDir(f.ConfigRoot()),
		daemon.WithSnapshotDir(f.SnapshotRoot()),
		daemon.WithLogDir(f.logDir),
		daemon.WithImageID(imageID),
		daemon.WithLogLevel(f.logLevel),
		daemon.WithLogToStdout(f.logToStdout),
		daemon.WithNydusdThreadNum(f.nydusdThreadNum),
	)
	if err != nil {
		return nil, err
	}
	err = f.manager.NewDaemon(d)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (f *Filesystem) Mount(ctx context.Context, snapshotID string, labels map[string]string) error {
	imageID, ok := labels[label.CRIImageRef]
	if !ok {
		return fmt.Errorf("failed to find image ref of snapshot %s, labels %v", snapshotID, labels)
	}
	d, err := f.createNewDaemon(snapshotID, imageID)
	// if daemon already exists for snapshotID, just return
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	defer func() {
		if err != nil {
			logrus.WithError(err).Warn("failed to mount")
			_ = f.manager.DestroyDaemon(d)
		}
	}()
	err = f.mount(d, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to start daemon %s", d.ID)
	}
	return nil
}

func (f *Filesystem) BootstrapFile(id string) (string, error) {
	panic("stargz has no bootstrap file")
}

func (f *Filesystem) NewDaemonConfig(labels map[string]string) (config.DaemonConfig, error) {
	panic("implement me")
}

func (f *Filesystem) mount(d *daemon.Daemon, labels map[string]string) error {
	err := f.generateDaemonConfig(d, labels)
	if err != nil {
		return err
	}
	return f.manager.StartDaemon(d)
}

func (f *Filesystem) generateDaemonConfig(d *daemon.Daemon, labels map[string]string) error {
	cfg, err := config.NewDaemonConfig(d.DaemonBackend, f.daemonCfg, d.ImageID, f.vpcRegistry, labels)
	if err != nil {
		return errors.Wrapf(err, "failed to generate daemon config for daemon %s", d.ID)
	}
	cfg.Device.Cache.Compressed = true
	cfg.DigestValidate = false
	return config.SaveConfig(cfg, d.ConfigFile())
}

func (f *Filesystem) WaitUntilReady(ctx context.Context, snapshotID string) error {
	d, err := f.manager.GetBySnapshotID(snapshotID)
	if err != nil {
		return err
	}

	return d.WaitUntilReady()
}

func (f *Filesystem) Umount(ctx context.Context, mountPoint string) error {
	id := filepath.Base(mountPoint)
	log.G(ctx).Infof("umount nydus daemon of id %s, mountpoint %s", id, mountPoint)
	return f.manager.DestroyBySnapshotID(id)
}

func (f *Filesystem) Cleanup(ctx context.Context) error {
	for _, d := range f.manager.ListDaemons() {
		err := f.Umount(ctx, filepath.Dir(d.MountPoint()))
		if err != nil {
			log.G(ctx).Infof("failed to umount %s err %+v", d.MountPoint(), err)
		}
	}
	return nil
}

func (f *Filesystem) MountPoint(snapshotID string) (string, error) {
	if d, err := f.manager.GetBySnapshotID(snapshotID); err == nil {
		return d.MountPoint(), nil
	}
	return "", fmt.Errorf("failed to find mountpoint of snapshot %s", snapshotID)
}
