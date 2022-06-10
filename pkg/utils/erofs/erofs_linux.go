//go:build linux
// +build linux

package erofs

import (
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func Mount(bootstrapPath, fsID, mountPoint string) error {
	mount := unix.Mount

	opts := "fsid=" + fsID
	logrus.Infof("Mount erofs to %s with options %s", mountPoint, opts)
	if err := mount("erofs", mountPoint, "erofs", 0, opts); err != nil {
		return errors.Wrapf(err, "failed to mount erofs")
	}

	return nil
}

func Umount(mountPoint string) error {
	return unix.Unmount(mountPoint, 0)
}

func FscacheID(imageID string) string {
	return digest.FromString(imageID).Hex()
}
