# Nydus Snapshotter

<p><img src="https://github.com/dragonflyoss/image-service/blob/master/misc/logo.svg" width="170"></p>

Nydus-snapshotter is a **non-core** sub-project of containerd.

Nydus snapshotter is an external plugin of containerd for [Nydus image service](https://nydus.dev) which implements a chunk-based content-addressable filesystem on top of a called `RAFS (Registry Acceleration File System)` format that improves the current OCI image specification, in terms of container launching speed, image space, and network bandwidth efficiency, as well as data integrity with several runtime backends: FUSE, virtiofs and in-kernel [EROFS](https://www.kernel.org/doc/html/latest/filesystems/erofs.html).

Nydus supports lazy pulling feature since pulling image is one of the time-consuming steps in the container lifecycle. Lazy pulling here means a container can run even the image is partially available and necessary chunks of the image are fetched on-demand. Apart from that, Nydus also supports [(e)Stargz](https://github.com/containerd/stargz-snapshotter) lazy pulling directly **WITHOUT** any explicit conversion.

For more details about how to build Nydus container image, please refer to [nydusify](https://github.com/dragonflyoss/image-service/blob/master/docs/nydusify.md) conversion tool and [acceld](https://github.com/goharbor/acceleration-service).

## Building

Just invoke `make` and check out the output executable binary `./bin/containerd-nydus-grpc`

```bash
make
```

## Integrate Nydus-snapshotter into Containerd

Containerd provides a general mechanism to exploit different types of snapshotters. Please ensure your containerd's version is 1.4.0 or above.
Add Nydus as a proxy plugin into containerd's configuration file which may be located at `/etc/containerd/config.toml`.

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

`nydusd-fusedev` is FUSE userspace daemon handling linux kernel FUSE requests from `/dev/fuse` frontend.
`nydusd-virtiofs` is a virtiofs daemon handling guest kernel FUSE requests.

## Configure Nydus

Nydus is configured by a json file which is required now. Since Nydus container images are likely stored in a registry, where auth has to be provided.
Please follow instructions to [configure nydus](./docs/configure_nydus.md) configure Nydus in order to make it work properly in your environment.

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
# `root` is the path to Nydus snapshotter
# `config-path` is the path to Nydus configuration file
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

You could append `--enable-stargz` to the command line above in order to enable (e)Stargz support.

### Validate Nydus-snapshotter Setup

Utilize containerd's `ctr` CLI command to validate if nydus-snapshotter is set up successfully.

```bash
$ ctr -a /run/containerd/containerd.sock plugin ls
TYPE                            ID                       PLATFORMS      STATUS
io.containerd.snapshotter.v1    nydus                    -              ok
```

## Quickly Start Container with Lazy Pulling

### Start Container on Node

Containerd can start container with specified snapshotter, so `nerdctl` or `ctr` needs to specify the Nydus snapshotter when start container.

A CLI tool [ctr-remote](https://github.com/dragonflyoss/image-service/tree/master/contrib/ctr-remote) is alongside. Use Nydus `ctr-remote` to pull Nydus image or start container based on nydus image.

```bash
$ sudo ctr-remote image rpull ghcr.io/dragonflyoss/image-service/nginx:nydus-latest
fetching sha256:75002dfe... application/vnd.oci.image.manifest.v1+json
fetching sha256:5a42e21c... application/vnd.oci.image.config.v1+json
fetching sha256:eb1af2e1... application/vnd.oci.image.layer.v1.tar+gzip

# Start container by `ctr-remote`
$ sudo ctr-remote run --snapshotter nydus ghcr.io/dragonflyoss/image-service/nginx:nydus-latest awesome-nydus

# Start container by `nerdctl`
nerdctl --snapshotter nydus run ghcr.io/dragonflyoss/image-service/nginx:nydus-latest
```

In addition that, `nerdctl` can now directly pull Nydus or (e)Stargz images with Nydus snapshotter without `ctr-remote` involved:

```bash
# Start an eStargz container with Nydus snapshotter by `nerdctl`
nerdctl --snapshotter nydus run -it --rm ghcr.io/stargz-containers/fedora:35-esgz
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
We can also use the `nydus-snapshotter` container image when we want to put Nydus stuffs inside a container. See the [nydus-snapshotter exmple](./misc/example/README.md) for how to setup and use it.

## Community

Nydus aims to form a **vendor-neutral opensource** image distribution solution to all communities.
Questions, bug reports, technical discussion, feature requests and contribution are always welcomed!

Join our Slack [workspace](https://join.slack.com/t/nydusimageservice/shared_invite/zt-pz4qvl4y-WIh4itPNILGhPS8JqdFm_w)
