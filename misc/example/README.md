## Example to setup and test nydus-snapshotter

The directory holds a few example config files to setup a nydus POC environment that saves its all state files in `/var/lib/containerd-test`, as specified in `containerd-test-config.toml`.
For now it contains following files:

* crictl.yaml: `crictl` tool config
* containerd-config.toml: `containerd` config
* containerd-config-v1.toml: `containerd` config in the deprecated v1 format, provided for old containerd versions
* containerd-test-config.toml: `containerd` config for testing
* 10-containerd-net.conflist: `containerd` cni config
* pod.yaml: pod spec yaml to be used by `crictl`
* container.yaml: container spec yaml to be used by `crictl`

With these config files, users can setup a nydus environment by doing:
```
export CNI_VERSION=v1.1.0
export CRICTL_VERSION=v1.23.0
sudo mkdir -p /var/lib/containerd-test/ /etc/containerd/ /opt/cni/bin/ /etc/cni/net.d/
sudo cp misc/example/containerd-test-config.toml /etc/containerd/
sudo cp misc/example/crictl.yaml /etc/
sudo cp misc/example/10-containerd-net.conflist /etc/cni/net.d/
# install cni plugin
wget https://github.com/containernetworking/plugins/releases/download/$CNI_VERSION/cni-plugins-linux-amd64-$CNI_VERSION.tgz
sudo tar xzf cni-plugins-linux-amd64-$CNI_VERSION.tgz -C /opt/cni/bin/
# install crictl
wget https://github.com/kubernetes-sigs/cri-tools/releases/download/$CRICTL_VERSION/crictl-$CRICTL_VERSION-linux-amd64.tar.gz
tar xzf crictl-$CRICTL_VERSION-linux-amd64.tar.gz -C /usr/local/bin/
# install nydus-overlayfs
NYDUS_VER=v$(curl -s "https://api.github.com/repos/dragonflyoss/nydus/releases/latest" | jq -r .tag_name | sed 's/^v//')
wget https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VER/nydus-static-$NYDUS_VER-linux-amd64.tgz
tar xzf nydus-static-$NYDUS_VER-linux-amd64.tgz
sudo cp nydus-static/nydus-overlayfs /usr/local/sbin/
```

Then start a `nydus-snapshotter` container with:
```
docker run -d --device /dev/fuse --cap-add SYS_ADMIN --security-opt apparmor:unconfined -e CONTAINERD_ROOT=/var/lib/containerd-test -v /var/lib/containerd-test:/var/lib/containerd-test:shared ghcr.io/containerd/nydus-snapshotter/nydus-snapshotter:latest
```
It will bindmount host directory `/var/lib/containerd-test` inside the container and share all the nydus mountpoints from there.
`containerd` config in `containerd-test-config.toml` has been setup to use the `nydus-snapshotter` grpc unix domain socket at `/var/lib/containerd-test/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock`.

Then start containerd with:
```
sudo containerd --config /etc/containerd/containerd-test-config.toml
```

Now we can use `crictl` to test against the POC environment.

## Deploy in production
When deploying `nydus-snapshotter` in production, following changes need to be applied:
1. remove the `CONTAINERD_ROOT` environment variable when starting `nydus-snapshotter` container, and bindmount `/var/lib/containerd` to `/var/lib/containerd` in the container.
2. use `containerd-config.toml` instead of `containerd-test-config.toml` to start containerd.
