//go:build !linux
// +build !linux

package erofs

import "fmt"

func Mount(bootstrapPath, fsID, mountPoint string) error {
	return fmt.Errorf("not implemented")
}

func Umount(mountPoint string) error {
	return fmt.Errorf("not implemented")
}

func FscacheID(snapshotID string) string {
	panic("not implemented")
}
