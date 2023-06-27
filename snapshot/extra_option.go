/*
 * Copyright (c) 2023. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package snapshot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots/storage"
	"github.com/containerd/nydus-snapshotter/config/daemonconfig"
	"github.com/containerd/nydus-snapshotter/pkg/daemon"
	"github.com/containerd/nydus-snapshotter/pkg/layout"
	"github.com/pkg/errors"
)

const (
	// Prepare a whole image as a nydus filesystem
	ModeNydusImageFs = 0
	// Prepare an image layer as a nydus filesystem
	ModeNydusLayerFs = 1
	// Prepare a whole image as a nydus block disk image
	ModeNydusImageBlock = 2
	// Prepare an image layer as a nydus block disk image
	ModeNydusLayerBlock = 3
	// Prepare a whole image as a raw block disk image
	ModeRawImageBlock = 4
	// Prepare an image layer as a raw block disk image
	ModeRawLayerBlock = 5
	// Relay image pull information to container runtime/agent, they will take the responsibility to pull image
	ModeRunteAgentPull = 6
	// The `Mount` is for native Linux container only, Kata/Coco should ignore/skip it.
	ModeIgnore = 7
)

// It's very expensive to build direct communication channel for extra information between the nydus snapshotter and
// kata-runtime/kata-agent/image-rs. So an extra mount option is used to pass information from nydus snapshotter to
// those components through containerd.
//
// The `Type` field determines the way to interpret other fields as below:
// Type: ModeNydusImageFs/ModeNydusLayerFs
// - Source: path the image meta blob
// - Config: nydusd configuration information
// - Snapshotdir: snapshot data directory
// - Version: RAFS filesystem version
// - Verity: unused
// Type: ModeNydusImageBlock/ModeNydusLayerBlock
// - Source: path to the image meta blob
// - Config: nydusd configuration information
// - Snapshotdir: snapshot data directory
// - Version: unused
// - Verity: data verity information
// Type: ModeRawImageBlock/ModeRawLayerBlock
// - Source: path to the raw block image for the whole container image
// - Config: unsued
// - Snapshotdir: unused
// - Version: unused
// - Verity: data verity information
// Type: ModeRunteAgentPull
// - Source: unused
// - Config: labels associated with the images, containing all labels from containerd
// - Snapshotdir: unused
// - Version: unused
// - Verity: unused
// Type: ModeIgnore
// - Source: unused
// - Config: unused
// - Snapshotdir: unused
// - Version: unused
// - Verity: unused
type ExtraOption struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Config      string `json:"config"`
	Snapshotdir string `json:"snapshotdir"`
	Version     string `json:"fs_version,omitempty"`
	Verity      string `json:"verity,omitempty"`
}

func (o *snapshotter) remoteMountWithExtraOptions(ctx context.Context, s storage.Snapshot, id string, overlayOptions []string) ([]mount.Mount, error) {
	source, err := o.fs.BootstrapFile(id)
	if err != nil {
		return nil, err
	}

	instance := daemon.RafsSet.Get(id)
	if instance == nil {
		return nil, errors.Errorf("can not find RAFS instance for snapshot %s", id)
	}
	daemon, err := o.fs.GetDaemonByID(instance.DaemonID)
	if err != nil {
		return nil, errors.Wrapf(err, "get daemon with ID %s", instance.DaemonID)
	}

	var c daemonconfig.DaemonConfig
	if daemon.IsSharedDaemon() {
		c, err = daemonconfig.NewDaemonConfig(daemon.States.FsDriver, daemon.ConfigFile(instance.SnapshotID))
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to load instance configuration %s",
				daemon.ConfigFile(instance.SnapshotID))
		}
	} else {
		c = daemon.Config
	}
	configContent, err := c.DumpString()
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal config")
	}

	// get version from bootstrap
	f, err := os.Open(source)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: check bootstrap version: failed to open bootstrap")
	}
	defer f.Close()
	header := make([]byte, 4096)
	sz, err := f.Read(header)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: check bootstrap version: failed to read bootstrap")
	}
	version, err := layout.DetectFsVersion(header[0:sz])
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to detect filesystem version")
	}

	// when enable nydus-overlayfs, return unified mount slice for runc and kata
	extraOption := &ExtraOption{
		Source:      source,
		Config:      configContent,
		Snapshotdir: o.snapshotDir(s.ID),
		Version:     version,
	}

	no, err := json.Marshal(extraOption)
	if err != nil {
		return nil, errors.Wrapf(err, "remoteMounts: failed to marshal NydusOption")
	}
	// XXX: Log options without extraoptions as it might contain secrets.
	log.G(ctx).Debugf("fuse.nydus-overlayfs mount options %v", overlayOptions)
	// base64 to filter easily in `nydus-overlayfs`
	opt := fmt.Sprintf("extraoption=%s", base64.StdEncoding.EncodeToString(no))
	overlayOptions = append(overlayOptions, opt)

	return []mount.Mount{
		{
			Type:    "fuse.nydus-overlayfs",
			Source:  "overlay",
			Options: overlayOptions,
		},
	}, nil
}
