package rootfspersister

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
	"github.com/containerd/nydus-snapshotter/pkg/label"
	"github.com/pkg/errors"
)

const defaultFSType = "ext4"

type ContainerInfo struct {
	namespace          string
	name               string
	sandboxAnnotations map[string]string
}

func GetContainerInfo(ctx context.Context, criRuntimeServiceEndpoint, containerID string) (*ContainerInfo, error) {
	criContainer, err := GetContainerFromCRI(ctx, criRuntimeServiceEndpoint, containerID)
	if err != nil {
		return nil, errors.Wrapf(err, "get container from CRI, id=%s", containerID)
	}

	var info ContainerInfo
	info.namespace, info.name, err = GetContainerNamespaceAndName(criContainer)
	if err != nil {
		return nil, errors.Wrapf(err, "get container namespace and name")
	}

	// NRI 0.1 does not pass pod annotations to plugin when being invoked, so query it from CRI runtime service.
	sandbox, err := GetSandboxFromCRI(ctx, criRuntimeServiceEndpoint, criContainer.PodSandboxId)
	if err != nil {
		return nil, errors.Wrapf(err, "get sandbox, id=%s", criContainer.PodSandboxId)
	}

	info.sandboxAnnotations = sandbox.Annotations

	return &info, nil
}

func TryGetContainerPersistSnapshotDesc(ctx context.Context, containerID, containerName string, podAnnotations map[string]string) (*WritableLayerPersistanceDesc, error) {
	log := Logger(ctx)

	v, ok := podAnnotations[VKERootfsWritableLayerPersistSpecAnnotation]
	if !ok {
		log.Debugf("Rootfs writable layer persistance spec does not exist")
		// nolint: nilnil
		return nil, nil
	}

	spec := make(WritableLayerPersistanceSpec, 0, 4)
	if err := json.Unmarshal([]byte(v), &spec); err != nil {
		return nil, errors.Wrapf(err, "unmarshal spec")
	}

	matchedIndex := -1
	for i, s := range spec {
		if s.ContainerName == containerName {
			matchedIndex = i
			break
		}
	}

	if matchedIndex < 0 {
		log.Infof("No need to persist writable layer for the container %s", containerName)
		// nolint: nilnil
		return nil, nil
	}

	return &spec[matchedIndex], nil
}

func HandlePostCreationEvent(ctx context.Context, pid int, containerdAddress, criRuntimeServiceEndpoint, containerID string) error {
	log := Logger(ctx)

	containerInfo, err := GetContainerInfo(ctx, criRuntimeServiceEndpoint, containerID)
	if err != nil {
		return errors.Wrapf(err, "get container info, id=%s", containerID)
	}

	desc, err := TryGetContainerPersistSnapshotDesc(ctx, containerID, containerInfo.name, containerInfo.sandboxAnnotations)
	if err != nil {
		return errors.Wrapf(err, "get persist snapshot desc, id=%s", containerID)
	}
	if desc == nil {
		// TODO: debug level
		log.Infof("No need to handle postCreate for container %s", containerID)
		return nil
	}

	writableLayerPath := desc.WritableLayerPath
	if writableLayerPath == "" {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "no writable layer path")
	}

	if !strings.HasPrefix(writableLayerPath, "/") {
		return errors.Wrapf(errdefs.ErrInvalidArgument, "writable layer path must start with /")
	}

	container, err := GetContainerFromContainerd(ctx, containerdAddress, containerID)
	if err != nil {
		return errors.Wrapf(err, "get container from containerd, id=%s", containerID)
	}

	envs, err := GetProcessEnvs(ctx, pid)
	if err != nil {
		return errors.Wrapf(err, "get process envs, pid=%d", pid)
	}

	devPath, err := GetDevicePathFromPVC(ctx, pid, envs, containerInfo.namespace, desc.PVCName)
	if err != nil {
		return errors.Wrapf(err, "get device path from the pvc")
	}

	mountpoint := BuildSnapshotMountpoint(containerID)
	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		return errors.Wrapf(err, "create mountpoint %q", mountpoint)
	}

	log.Infof("Mount %q to %q", devPath, mountpoint)

	fsType, err := ProbeFilesystem(ctx, devPath)
	if err != nil {
		return errors.Wrapf(err, "probe file system on device %s", devPath)
	} else if fsType == "" {
		fsType = defaultFSType
		if err := FormatDisk(ctx, fsType, devPath, nil); err != nil {
			return errors.Wrapf(err, "format disk %s", devPath)
		}
	}

	err = Mount(fsType, devPath, mountpoint)
	if err != nil {
		log.Errorf("Failed to mount: %v", err)
		return errors.Wrapf(err, "mount ext4 at %s", mountpoint)
	}

	defer func() {
		if err != nil {
			if err = Umount(mountpoint); err != nil {
				log.Errorf("Failed to umount %s: %v", mountpoint, err)
			}
		}
	}()

	snapshotDir := filepath.Join(mountpoint, desc.WritableLayerPath)
	fsDir := filepath.Join(snapshotDir, "fs")
	workDir := filepath.Join(snapshotDir, "workdir")

	err = os.MkdirAll(fsDir, 0755)
	if err != nil {
		return errors.Wrapf(err, "create fsdir: %q", fsDir)
	}

	err = os.MkdirAll(workDir, 0755)
	if err != nil {
		return errors.Wrapf(err, "create workdir: %q", workDir)
	}

	var snapshotUpdater *SnapshotUpdater
	snapshotUpdater, err = NewSnapshotUpdater(containerdAddress, container.Snapshotter)
	if err != nil {
		return errors.Wrapf(err, "create snapshot updater")
	}

	// Should found the EBS disk and mount it to local tree.
	err = snapshotUpdater.AddLabelToSnapshot(ctx, container.SnapshotKey,
		map[string]string{
			label.RootfsWritableLayerPath: filepath.Join(mountpoint, desc.WritableLayerPath),
		})
	if err != nil {
		return errors.Wrapf(err, "add label to snapshot %s", container.SnapshotKey)
	}

	return nil
}
