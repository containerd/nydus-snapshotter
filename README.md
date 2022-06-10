# Nydus Snapshotter

<p><img src="https://github.com/dragonflyoss/image-service/blob/master/misc/logo.svg" width="170"></p>

Nydus-snapshotter is a **non-core** sub-project of containerd.

Pulling and unpacking OCI container image are time consuming when start a container. [Nydus](https://github.com/dragonflyoss/image-service) is a vendor-neutral project aiming at mitigating the problem. It designs a new container image oriented and optimized file system format with capability of on-demand read. For instructions on how to build nydus container image, please refer to [nydusify](https://github.com/dragonflyoss/image-service/blob/master/docs/nydusify.md) conversion tool.

## Building

Just invoke `make` and find output binary `./bin/containerd-nydus-grpc`

```bash
make
```

## Integrate Nydus-snapshotter into Containerd

Containerd provides a general mechanism to exploit different types of snapshotters. Please ensure your containerd's version is beyond 1.4.0.
Add nydus as a proxy plugin into containerd's configuration file which may be located at `/etc/containerd/config.toml`.

```toml
# The `address` field specifies through which socket snapshotter and containerd communicate.
[proxy_plugins]
  [proxy_plugins.nydus]
    type = "snapshot"
    address = "/run/containerd-nydus/containerd-nydus-grpc.sock"
```

Restart your containerd service making the change take effect. Assume that your node is systemd based, restart the service as below:

```bash
systemctl restart containerd
```

## Get Nydusd Binary

Find a suitable `nydusd` release for you from [nydus releases page](https://github.com/dragonflyoss/image-service/releases).

`nydusd-fusedev` is FUSE userspace daemon handling linux kernel fuse requests from `/dev/fuse` frontend.
`nydusd-virtiofs` is a virtiofs daemon handling guest kernel fuse requests.

## Configure Nydus

Nydus is configured by a json file which is required now. Because nydus container images are likely stored in a registry, where auth has to be provided.
Please follow instructions to [configure nydus](./docs/configure_nydus.md) configure nydus making it work properly in your environment.

## Start Nydus Snapshotter

Nydus-snapshotter is implemented as a [proxy plugin](https://github.com/containerd/containerd/blob/04985039cede6aafbb7dfb3206c9c4d04e2f924d/PLUGINS.md#proxy-plugins) (`containerd-nydus-grpc`) for containerd.

Assume your server systemd based, install nydus-snapshotter:
Note: `nydusd` and `nydus-image` should be found from $PATH.
```bash
make install
systemctl restart containerd
```

Or you can start nydus-snapshotter manually.
```bash
# `nydusd-path` is the path to nydusd binary
# `address` is the domain socket that you configured in containerd configuration file
# `root` is the path to nydus snapshotter
# `config-path` is the path to nydus configuration file
$ ./containerd-nydus-grpc \
    --config-path /etc/nydusd-config.json \
    --shared-daemon \
    --log-level info \
    --root /var/lib/containerd/io.containerd.snapshotter.v1.nydus \
    --cache-dir /var/lib/nydus/cache \
    --address /run/containerd-nydus/containerd-nydus-grpc.sock \
    --nydusd-path /usr/local/bin/nydusd \
    --nydusimg-path /usr/local/bin/nydus-image \
    --log-to-stdout
```

### Validate Nydus-snapshotter Setup

Utilize containerd's `ctr` CLI command to validate if nydus-snapshotter is set up successfully.

```bash
$ ctr -a /run/containerd/containerd.sock plugin ls
TYPE                            ID                       PLATFORMS      STATUS
io.containerd.snapshotter.v1    nydus                    -              ok
```

## Quickly Start Container with Lazy Pulling

### Start Container on Node

Containerd can start container with specified snapshotter, so legacy method like `nerdctl` or `ctr` needs to specify the `nydus` snapshotter when start container.
A CLI tool [ctr-remote](https://github.com/dragonflyoss/image-service/tree/master/contrib/ctr-remote) is alongside. Use nydus `ctr-remote` to pull nydus image or start container based on nydus image.

```bash
$ sudo ctr-remote image rpull ghcr.io/dragonflyoss/image-service/nginx:nydus-latest
fetching sha256:75002dfe... application/vnd.oci.image.manifest.v1+json
fetching sha256:5a42e21c... application/vnd.oci.image.config.v1+json
fetching sha256:eb1af2e1... application/vnd.oci.image.layer.v1.tar+gzip

# Start container by `ctr-remote`
$ sudo ctr-remote run --snapshotter nydus ghcr.io/dragonflyoss/image-service/nginx:nydus-latest awsome-nydus

# Start container by `nerdctl`
nerdctl --snapshotter nydus run ghcr.io/dragonflyoss/image-service/nginx:nydus-latest
```

### Start Container in Kubernetes

**NOTE:** A potential drawback using CRI is that we can hardly specify snapshotter to `nydus-snapshotter`. So we have to change containerd's default snapshotter in its configuration file and enable snapshot annotations like below:

```toml
[plugins."io.containerd.grpc.v1.cri".containerd]
   snapshotter = "nydus"
   disable_snapshot_annotations = false
```

Use `crictl` to debug starting container via Kubernetes CRI. Dry run [steps](./docs/crictl_dry_run.md) of using `crictl` can be found in [documents](./docs).

### Setup with nydus-snapshotter image
We can also use the `nydus-snapshotter` container image when we want to put nydus staff inside a container. See the [nydus-snapshotter exmple](./misc/example/README.md) for how to setup and use it.

## Community

Nydus aims to form a **vendor-neutral opensource** image distribution solution to all communities.
Questions, bug reports, technical discussion, feature requests and contribution are always welcomed!

Join our Slack [workspace](https://join.slack.com/t/nydusimageservice/shared_invite/zt-pz4qvl4y-WIh4itPNILGhPS8JqdFm_w)
