/*
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package erofs

import (
	"fmt"

	"github.com/containerd/containerd/log"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func Mount(bootstrapPath, domainID, fscacheID, mountPoint string) error {
	mount := unix.Mount
	var opts string

	// Nydusd must have domain_id specified and it is set to fsid if it is
	// never specified.
	if domainID != "" && domainID != fscacheID {
		opts = fmt.Sprintf("domain_id=%s,fsid=%s", domainID, fscacheID)
	} else {
		opts = "fsid=" + fscacheID
	}
	log.L.Infof("Mount erofs to %s with options %s", mountPoint, opts)

	if err := mount("erofs", mountPoint, "erofs", 0, opts); err != nil {
		if errors.Is(err, unix.EINVAL) && domainID != "" {
			log.L.Errorf("mount erofs with shared domain failed," +
				"If using this feature, make sure your Linux kernel version >= 6.1")
		}
		return errors.Wrapf(err, "failed to mount erofs")
	}

	return nil
}

func Umount(mountPoint string) error {
	return unix.Unmount(mountPoint, 0)
}

func FscacheID(snapshotID string) string {
	return digest.FromString(fmt.Sprintf("nydus-snapshot-%s", snapshotID)).Hex()
}
