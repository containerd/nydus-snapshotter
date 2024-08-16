package rootfspersister

import (
	"context"
	"os"
	"syscall"

	"github.com/pkg/errors"
)

func HandleRemovalEvent(ctx context.Context, criRuntimeServiceEndpoint, containerID string) error {
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
		log.Infof("No need to handle removal event for container %s", containerID)
		return nil
	}

	if err := func() error {
		mountpoint := BuildSnapshotMountpoint(containerID)
		log.Infof("Unmount %s for container %s", mountpoint, containerID)
		// We don't check if it is mounted since the checking method may get error when the
		// disk has already been detached.
		if err := Umount(mountpoint); err != nil {
			if errors.Is(err, syscall.EINVAL) {
				log.Warningf("The path %s is not mounted", mountpoint)
				return nil
			}

			if errors.Is(err, syscall.ENOENT) {
				log.Warningf("The mountpoint %s can't be found: %v", mountpoint, err)
				return nil
			}

			return errors.Wrapf(err, "umount %s", mountpoint)
		}

		return nil
	}(); err != nil {
		return err
	}

	if err := os.RemoveAll(SnapshotWorkDir(containerID)); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return errors.Wrapf(err, "remove snapshot workdir %s", SnapshotWorkDir(containerID))
		}
	}

	return nil
}
