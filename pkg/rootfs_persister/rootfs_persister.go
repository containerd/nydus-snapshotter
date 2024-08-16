package rootfspersister

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	cri "k8s.io/cri-client/pkg"

	"github.com/containerd/nydus-snapshotter/pkg/errdefs"
)

const (
	K8sContainerNameAnnotationKey    = "io.kubernetes.container.name"
	K8sSandboxNamespaceAnnotationKey = "io.kubernetes.pod.namespace"
)

const VKERootfsWritableLayerPersistSpecAnnotation = "vke.volcengine.com/rootfs-writable-layer-persist-spec"

type ctxLogger struct{}

func WithLogger(ctx context.Context, logger *logrus.Logger) context.Context {
	return context.WithValue(ctx, ctxLogger{}, logger)
}

func Logger(ctx context.Context) *logrus.Logger {
	if logger, ok := ctx.Value(ctxLogger{}).(*logrus.Logger); ok {
		return logger
	}
	return logrus.StandardLogger()
}

type WritableLayerPersistanceDesc struct {
	ContainerName     string `json:"containerName"`
	PVCName           string `json:"pvcName"`
	WritableLayerPath string `json:"writableLayerPath"`
}

type WritableLayerPersistanceSpec = []WritableLayerPersistanceDesc

type SnapshotUpdater struct {
	containerdClient *containerd.Client
	snapshotter      string
}

func getContainerNameFromLabels(annotations map[string]string) string {
	return annotations[K8sContainerNameAnnotationKey]
}

func getContainerNamespaceFromAnnotation(annotations map[string]string) string {
	return annotations[K8sSandboxNamespaceAnnotationKey]
}

func GetContainerFromContainerd(ctx context.Context, containerdAddress, id string) (*containers.Container, error) {
	containerdClient, err := containerd.New(containerdAddress, containerd.WithDefaultNamespace("k8s.io"))
	if err != nil {
		return nil, errors.Wrapf(err, "new containerd client, address=%q", containerdAddress)
	}

	container, err := containerdClient.ContainerService().Get(ctx, id)
	if err != nil {
		return nil, errors.Wrapf(err, "get container %s", id)
	}

	return &container, nil
}

func GetContainerFromCRI(ctx context.Context, criRuntimeServiceEndpoint, id string) (*runtimeapi.Container, error) {
	criClient, err := cri.NewRemoteRuntimeService(criRuntimeServiceEndpoint, 5*time.Second, nil, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "new CRI runtime client, address=%s", criRuntimeServiceEndpoint)
	}

	containers, err := criClient.ListContainers(ctx, &runtimeapi.ContainerFilter{Id: id})
	if err != nil {
		return nil, errors.Wrapf(err, "list containers, id=%s", id)
	}

	if len(containers) != 1 {
		return nil, errors.Wrap(errdefs.ErrFailedPrecondition, "must only have one container")
	}

	container := containers[0]

	return container, nil
}

func GetSandboxFromCRI(ctx context.Context, criRuntimeServiceEndpoint, sandboxID string) (*runtimeapi.PodSandbox, error) {
	criClient, err := cri.NewRemoteRuntimeService(criRuntimeServiceEndpoint, 5*time.Second, nil, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "new CRI runtime client, address=%s", criRuntimeServiceEndpoint)
	}

	sandboxes, err := criClient.ListPodSandbox(ctx, &runtimeapi.PodSandboxFilter{Id: sandboxID})
	if err != nil {
		return nil, errors.Wrapf(err, "list sandbox")
	}

	if len(sandboxes) != 1 {
		return nil, errors.Wrapf(errdefs.ErrFailedPrecondition, "must only have one sandbox")
	}

	sandbox := sandboxes[0]

	return sandbox, nil
}

func GetContainerNamespaceAndName(c *runtimeapi.Container) (string, string, error) {
	containerLabels := c.Labels

	namespace := getContainerNamespaceFromAnnotation(containerLabels)
	if namespace == "" {
		return "", "", errors.Wrapf(errdefs.ErrNotFound, "get container namespace from labels")
	}

	name := getContainerNameFromLabels(containerLabels)
	if name == "" {
		return "", "", errors.Wrapf(errdefs.ErrNotFound, "get container name from labels")
	}

	return namespace, name, nil
}

func GetProcessEnvs(_ context.Context, pid int) ([]string, error) {
	envPath := fmt.Sprintf("/proc/%d/environ", pid)
	environOut, err := os.ReadFile(envPath)
	if err != nil {
		return nil, errors.Wrapf(err, "read envs from %q", envPath)
	}

	return strings.Split(string(environOut), string(rune(0))), nil
}

func SnapshotWorkDir(containerID string) string {
	return filepath.Join("/var/run", "rootfs-persister", containerID)
}

func BuildSnapshotMountpoint(containerID string) string {
	return filepath.Join(SnapshotWorkDir(containerID), "mnt")
}
