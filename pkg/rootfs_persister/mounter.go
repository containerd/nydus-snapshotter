package rootfspersister

import (
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
)

func Mount(fsType, source, mountpoint string) error {
	mount := unix.Mount
	if err := mount(source, mountpoint, fsType, 0, ""); err != nil {
		return errors.Wrapf(err, "fsType=%s mount device=%s at %s", fsType, source, mountpoint)
	}

	return nil
}

func Umount(mountpoint string) error {
	return syscall.Unmount(mountpoint, 0)
}
