package rootfspersister

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/k8sclient"
)

func LocateDiskBySerialNumber(ctx context.Context, expected string) (string, error) {
	log := Logger(ctx)

	sysBlockDir := "/sys/block"

	blocks, err := os.ReadDir(sysBlockDir)
	if err != nil {
		return "", errors.Wrapf(err, "read sysfs blocks dir")
	}

	log.Infof("Attached blocks: %v", blocks)

	for _, b := range blocks {
		serialPath := filepath.Join(sysBlockDir, b.Name(), "serial")
		serial, err := os.ReadFile(serialPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", errors.Wrapf(err, "read serial number from %s", serialPath)
		}

		if string(serial) == expected {
			deviceNumberPath := filepath.Join(sysBlockDir, b.Name(), "dev")
			deviceNumber, err := os.ReadFile(deviceNumberPath)
			if err != nil {
				return "", errors.Wrapf(err, "read dev file %q", deviceNumberPath)
			}

			devPath := filepath.Join("/dev/block", strings.TrimSpace(string(deviceNumber)))

			return devPath, nil
		}
	}

	return "", errdefs.ErrNotFound
}

func GetBlockDevicePathFromPVC(ctx context.Context, namespace, pvcName string) (string, error) {
	log := Logger(ctx)

	log.Infof("Get block device path for PVC %s/%s", namespace, pvcName)

	serialNumber, err := GetBlockDeviceSerialNumberByPVC(ctx, namespace, pvcName)
	if err != nil {
		return "", errors.Wrapf(err, "get serial number")
	}

	retrials := 30
	var devPath string

	for retrials > 0 {
		devPath, err = LocateDiskBySerialNumber(ctx, serialNumber)
		if err == nil {
			break
		}

		if errors.Is(err, errdefs.ErrNotFound) {
			retrials--
			time.Sleep(2 * time.Second)
			log.Infof("Can't locate the disk having serial=%s, retrying...", serialNumber)
			continue
		}

		return "", errors.Wrapf(err, "get device path, serial=%s", serialNumber)
	}

	if devPath == "" {
		return "", errdefs.ErrNotFound
	}

	return devPath, nil
}

func GetBlockDeviceSerialNumberByPVC(ctx context.Context, namespace, pvcName string) (string, error) {
	c, err := k8sclient.NewKubeClient(10, 20)
	if err != nil {
		return "", errors.Wrapf(err, "create K8s client")
	}

	pvc, err := c.GetPersistentVolumeClaim(ctx, namespace, pvcName)
	if err != nil {
		return "", errors.Wrapf(err, "get pvc=%s", pvcName)
	}

	pvName := pvc.Spec.VolumeName
	pv, err := c.GetPersistentVolume(ctx, pvName)
	if err != nil {
		return "", errors.Wrapf(err, "get pv=%s", pvName)
	}

	volumeHandle := pv.Spec.CSI.VolumeHandle
	// TODO: Validate format
	serialNumber := strings.TrimLeft(volumeHandle, "vol-")

	return serialNumber, nil

}

// The NRI depends on EBS CSI to attach the remote disk, this method helps locate where the device path is
func GetDevicePathFromPVC(ctx context.Context, pid int, envs []string, namespace, pvcName string) (string, error) {
	log := Logger(ctx)

	reader, writer, err := os.Pipe()
	if err != nil {
		return "", errors.Wrapf(err, "create pipe")
	}
	defer reader.Close()
	defer writer.Close()

	attr := os.ProcAttr{
		Env: envs,
		Sys: &syscall.SysProcAttr{
			Chroot: fmt.Sprintf("/proc/%d/root", pid),
		},
		Files: []*os.File{
			// Logs will be redirected to containerd
			os.Stdin,
			writer,
			os.Stderr,
		},
	}

	cmd := []string{"/proc/self/exe", "locate", "--pvc-name", pvcName, "--pvc-namespace", namespace}
	process, err := os.StartProcess("/proc/self/exe", cmd, &attr)
	if err != nil {
		log.Errorf("Failed to start subprocess, cmd=%q: %v", cmd, err)
		return "", errors.Wrapf(err, "start subprocess")
	}

	outputWaiter := make(chan int)
	buf := make([]byte, 4096)
	go func() {
		n, err := reader.Read(buf)
		if err != nil {
			log.Errorf("Failed to read output from subprocess: %v", err)
		}
		outputWaiter <- n
	}()

	if st, err := process.Wait(); err != nil {
		log.Errorf("Failed to wait for process: %v", err)
	} else if st.ExitCode() != 0 {
		return "", errors.Wrapf(errdefs.ErrFailedPrecondition, "execute command: %s, status=%v", cmd, st)
	}

	n := <-outputWaiter
	if n <= 0 {
		return "", errors.New("no output from subprocess")
	}
	devPath := string(buf[0:n])

	return devPath, nil
}

func FormatDisk(ctx context.Context, fsType, device string, options []string) error {
	log := Logger(ctx)

	formatterBin := fmt.Sprintf("mkfs.%s", fsType)

	log.Infof("Formatting disk %s with fs type %s options %v", device, fsType, options)

	// resolve nydusd path
	if _, err := exec.LookPath(formatterBin); err != nil {
		return errors.Wrapf(err, "look up file system formatter %s", formatterBin)
	}

	args := make([]string, 0, 8)
	args = append(args, options...)
	args = append(args, device)

	cmd := exec.Command(formatterBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Errorf("Failed to execute command, output=%v: %v", string(output), err)
		return errors.Wrapf(err, "execute mkfs")
	}

	return nil
}

func ProbeFilesystem(ctx context.Context, device string) (fsType string, err error) {
	log := Logger(ctx)

	if _, err := exec.LookPath("wipefs"); err != nil {
		return "", errors.Wrapf(err, "look up wipefs tool")
	}

	/*
		output example:
		   0x438,97f776a9-d771-42b7-88ff-b723fcbd19ea,,ext4
	*/
	cmd := exec.Command("wipefs", "-p", device)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrapf(err, "execute command, output: %v", output)
	}

	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 {
		log.Infof("The block has no file system")
		return "", nil
	}

	tokens := strings.Split(trimmed, ",")
	if len(tokens) != 4 {
		return "", errors.Wrapf(errdefs.ErrFailedPrecondition, "len=%d, tokens: %#v, invalid output: %#v",
			len(tokens), tokens, output)
	}

	fsType = tokens[3]
	if fsType != "" {
		log.Infof("The disk has file system %s", fsType)
	}

	return
}
