//go:build linux
// +build linux

package erofs

import (
	"encoding/binary"
	"io"
	"os"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func getDevices(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open bootstrap")
	}
	defer f.Close()

	devices := make([]string, 0)
	_, err = f.Seek(1024, io.SeekStart)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to seek bootstrap")
	}
	byte4 := make([]byte, 4)
	_, err = f.Read(byte4)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read super magic")
	}
	if binary.LittleEndian.Uint32(byte4) != 0xe0f5e1e2 {
		return nil, errors.New("bad erofs magic")
	}
	_, err = f.Seek(1024+86, io.SeekStart)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to seek bootstrap")
	}
	_, err = f.Read(byte4)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read devt_slotoff")
	}
	nrDevices := binary.LittleEndian.Uint16(byte4[0:2])
	pos := int64(binary.LittleEndian.Uint16(byte4[2:4])) * 128
	for nrDevices > 0 {
		tag := make([]byte, 64)
		_, err = f.Seek(pos, io.SeekStart)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to seek bootstrap")
		}
		_, err = f.Read(tag)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read device tag")
		}
		devices = append(devices, string(tag))
		nrDevices = nrDevices - 1
		pos = pos + 128
	}
	return devices, nil
}

func Mount(bootstrapPath, fsID, mountPoint string) error {
	devices, err := getDevices(bootstrapPath)
	if err != nil {
		return errors.Wrap(err, "get erofs devices from bootstrap")
	}

	mount := unix.Mount

	opts := "fsid=" + fsID + ",device=" + strings.Join(devices, ",device=")
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
