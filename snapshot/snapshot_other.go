//go:build !linux
// +build !linux

package snapshot

import (
	"fmt"
	"os"
)

func getSupportsDType(dir string) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

func lchown(target string, st os.FileInfo) error {
	return fmt.Errorf("not implemented")
}
