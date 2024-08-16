package rootfspersister

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/pkg/errors"
)

func NewSnapshotUpdater(containerdAddress, snapshotter string) (*SnapshotUpdater, error) {
	containerdClient, err := containerd.New(containerdAddress, containerd.WithDefaultNamespace("k8s.io"))
	if err != nil {
		return nil, errors.Wrapf(err, "new containerd client")
	}

	return &SnapshotUpdater{
		containerdClient: containerdClient,
		snapshotter:      snapshotter,
	}, nil
}

func (su *SnapshotUpdater) AddLabelToSnapshot(ctx context.Context, snapshotKey string, labels map[string]string) error {
	log := Logger(ctx)

	snapshotterClient := su.containerdClient.SnapshotService(su.snapshotter)

	log.Infof("Add label %#v to snapshot %s", labels, snapshotKey)

	snapshot, err := snapshotterClient.Stat(ctx, snapshotKey)
	if err != nil {
		return errors.Wrapf(err, "stat snapshot key=%s", snapshotKey)
	}

	if snapshot.Labels == nil {
		snapshot.Labels = make(map[string]string)
	}

	paths := make([]string, 0, 4)
	for k, v := range labels {
		paths = append(paths, fmt.Sprintf("labels.%s", k))
		if v != "" {
			snapshot.Labels[k] = v
		}
	}

	_, err = snapshotterClient.Update(ctx, snapshot, paths...)
	if err != nil {
		return errors.Wrapf(err, "update snapshot")
	}

	return nil
}
