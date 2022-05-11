//go:build !linux
// +build !linux

package mount

import "fmt"

type Mounter struct {
}

func (m *Mounter) Umount(target string) error {
	return fmt.Errorf("not implemented")
}

func (m *Mounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}
