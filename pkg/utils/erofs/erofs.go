//go:build linux
// +build linux

package erofs

import (
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const GlobalDomainName = "global-domain"

func Mount(bootstrapPath, fsID, mountPoint string) error {
	mount := unix.Mount

	opts := fmt.Sprintf("domain_id=%s,fsid=%s", GlobalDomainName, fsID)
	logrus.Infof("Mount erofs to %s with options %s", mountPoint, opts)

	// If a mount with the option 'domain_id' fails, this indicates that
	// the kernel may not support EROFS shared domain feature.
	// Fallback to retry the mount without 'domain_id' option.
	if err := mount("erofs", mountPoint, "erofs", 0, opts); err != nil {
		if errors.Is(err, unix.EINVAL) {
			logrus.Infof("Fallback to the mount without @domain_id option")
			opts := "fsid=" + fsID
			err = mount("erofs", mountPoint, "erofs", 0, opts)
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
