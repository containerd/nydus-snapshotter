# Run Dragonfly & Nydus in Kubernetes

We recommend using the Dragonfly P2P data distribution system to further improve the runtime performance of Nydus images.

If you want to deploy Dragonfly and Nydus at the same time through Helm, please refer to the **[Quick Start](https://github.com/dragonflyoss/helm-charts/blob/main/INSTALL.md)**.

# Run Nydus snapshotter in Kubernetes

This document will introduce how to run Nydus snapshotter in Kubernetes cluster, you can use helm to deploy Nydus snapshotter container.

**NOTE:** This document is mainly to allow everyone to quickly deploy and experience Nydus snapshotter in the Kubernetes cluster. You cannot use it as a deployment and configuration solution for the production environment.

## Setup Kubernetes using kind

[kind](https://kind.sigs.k8s.io/) is a tool for running local Kubernetes clusters using Docker container “nodes”.
kind was primarily designed for testing Kubernetes itself, but may be used for local development or CI.

First, we need to prepare a configuration file(`kind-config.yaml`) for kind to specify the devices and files we need to mount to the kind node.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  ipFamily: dual
nodes:
  - role: control-plane
    image: kindest/node:v1.30.2
    extraMounts:
      - hostPath: ./containerd-config.toml
        containerPath: /etc/containerd/config.toml
      - hostPath: /dev/fuse
        containerPath: /dev/fuse
```

Next, we also need a config for containerd(`containerd-config.toml`).

**NOTE:** It may be necessary to explain here why `disable_snapshot_annotations` and `discard_unpacked_layers` need to be configured in containerd.
- `disable_snapshot_annotations`: This variable disables to pass additional annotations (image related information) to snapshotters in containerd (default value is `true`). In nydus snapshotter, we need these annotations to pull images. Therefore, we need to set it to `false`.
- `discard_unpacked_layers`: This variable allows GC to remove layers from the content store after successfully unpacking these layers to the snapshotter in containerd (default value is `true`). In nydus snapshotter, we need to preserve layers for demand pulling and sharing even after they are unpacked. Therefore, we need to set it to `false`.

```toml
version = 2
[debug]
  level = "debug"

[plugins."io.containerd.grpc.v1.cri".containerd]
  discard_unpacked_layers = false
  disable_snapshot_annotations = false
  snapshotter = "overlayfs"
  default_runtime_name = "runc"
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc]
  runtime_type = "io.containerd.runc.v2"

[plugins."io.containerd.grpc.v1.cri"]
  sandbox_image = "registry.k8s.io/pause:3.6"
```

With these two configuration files, we can create a kind cluster.

```bash
$ kind create cluster --config=kind-config.yaml
```

If everything is fine, kind should have prepared the configuration for us, and we can run the `kubectl` command directly on the host machine, such as:

```bash
$ kubectl get nodes
NAME                 STATUS   ROLES                  AGE   VERSION
kind-control-plane   Ready    control-plane,master   19m   v1.23.4
```

## Use helm to install Nydus snapshotter

Before proceeding, you need to make sure [`helm` is installed](https://helm.sh/docs/intro/quickstart/#install-helm).

First, you need to create a `config-nydus.yaml` for helm to install Nydus-snapshotter.

```yaml
name: nydus-snapshotter
pullPolicy: Always
hostNetwork: true
dragonfly:
  enable: false

containerRuntime:
  containerd:
    enable: true
```

Then clone the Nydus snapshotter helm chart.

```bash
$ git clone https://github.com/dragonflyoss/helm-charts.git
```

Last run helm to create Nydus snapshotter.

```bash
$ cd helm-charts
$ helm install --wait --timeout 10m --dependency-update \
    --create-namespace --namespace nydus-system \
    -f config-nydus.yaml \
    nydus-snapshotter charts/nydus-snapshotter
```

## Run Nydus containers

We can then create a Pod(`nydus-pod.yaml`) config file that runs Nydus image.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nydus-pod
spec:
  containers:
    - name: nginx
      image: ghcr.io/dragonflyoss/image-service/nginx:nydus-latest
      imagePullPolicy: Always
      command: ["sh", "-c"]
      args:
        - tail -f /dev/null
```

Use `kubectl` to create the Pod.

```bash
$ kubectl create -f nydus-pod.yaml
$ kubectl get pods -w
```

The `-w` options will block the console and wait for the changes of the status of the pod. If everything is normal, you can see that the pod will become `Running` after a while.

```bash
NAME       READY   STATUS              RESTARTS   AGE
nydus-pod  1/1     Running             0          51s
```
