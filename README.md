# Nydus Snapshotter

<p><img src="https://github.com/dragonflyoss/image-service/blob/master/misc/logo.svg" width="170"></p>

[![Release Version](https://img.shields.io/github/v/release/containerd/nydus-snapshotter?style=flat)](https://github.com/containerd/nydus-snapshotter/releases)
[![LICENSE](https://img.shields.io/github/license/containerd/nydus-snapshotter.svg?style=flat)](https://github.com/containerd/nydus-snapshotter/blob/main/LICENSE)
![CI](https://github.com/containerd/nydus-snapshotter/actions/workflows/ci.yml/badge.svg?event=push)
[![Go Report Card](https://goreportcard.com/badge/github.com/containerd/nydus-snapshotter?style=flat)](https://goreportcard.com/report/github.com/containerd/nydus-snapshotter)
[![Twitter](https://img.shields.io/twitter/url?style=social&url=https%3A%2F%2Ftwitter.com%2Fdragonfly_oss)](https://twitter.com/dragonfly_oss)
[![Nydus Stars](https://img.shields.io/github/stars/dragonflyoss/image-service?label=Nydus%20Stars&style=social)](https://github.com/dragonflyoss/image-service)

Nydus-snapshotter is a **non-core** sub-project of containerd.

Nydus snapshotter is an external plugin of containerd for [Nydus image service](https://nydus.dev) which implements a chunk-based content-addressable filesystem on top of a called `RAFS (Registry Acceleration File System)` format that improves the current OCI image specification, in terms of container launching speed, image space, and network bandwidth efficiency, as well as data integrity with several runtime backends: FUSE, virtiofs and in-kernel [EROFS](https://www.kernel.org/doc/html/latest/filesystems/erofs.html).

Nydus supports lazy pulling feature since pulling image is one of the time-consuming steps in the container lifecycle. Lazy pulling here means a container can run even the image is partially available and necessary chunks of the image are fetched on-demand. Apart from that, Nydus also supports [(e)Stargz](https://github.com/containerd/stargz-snapshotter) and OCI (by using zran) lazy pulling directly **WITHOUT** any explicit conversion.

For more details about how to build Nydus container image, please refer to [nydusify](https://github.com/dragonflyoss/image-service/blob/master/docs/nydusify.md) conversion tool and [acceld](https://github.com/goharbor/acceleration-service).

## Architecture Based on FUSE

![fuse arch](./docs/diagram/nydus_fuse_arch.svg)

## Architecture Based on Fscache/Erofs

![fscache arch](./docs/diagram/nydus_fscache_erofs_arch.svg)

## Building

Just invoke `make` and check out the output executable binary `./bin/containerd-nydus-grpc`

```bash
make
```

## Integrate Nydus-snapshotter into Containerd

The following document will describe how to manually configure containerd + Nydus snapshotter. If you want to run Nydus snapshotter in Kubernetes cluster, you can try to use helm or run nydus snapshotter as a container. You can refer to [this documentation](./docs/run_nydus_in_kubernetes.md).

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

## Get Nydus Binaries

Get `nydusd` `nydus-image` and `nydusctl` binaries from [nydus releases page](https://github.com/dragonflyoss/image-service/releases).
It's suggested to install the binaries to your system path. `nydusd` is FUSE userspace daemon and a vhost-user-fs backend. Nydus-snapshotter
will fork a nydusd process when necessary.

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
# `nydusd-path` is the path to nydusd binary. If `nydusd` and `nydus-image` are installed, `--nydusd-path` and `--nydusimage-path`can be omitted.
# Otherwise, provide them in below command line.
# `address` is the domain socket that you configured in containerd configuration file
# `config-path` is the path to Nydus configuration file
# The default nydus-snapshotter work directory is located at `/var/lib/containerd-nydus`

$ ./containerd-nydus-grpc \
    --config-path /etc/nydus/nydusd-config.json \
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

We can also use the `nydus-snapshotter` container image when we want to put Nydus stuffs inside a container. See the [nydus-snapshotter example](./misc/example/README.md) for how to setup and use it.

## Integrate with Dragonfly to Distribute Images in P2P

Nydus is also a sub-project of [Dragonfly](https://github.com/dragonflyoss/Dragonfly2). So it closely works with Dragonfly to distribute container images in a fast and efficient P2P fashion to reduce network latency and lower the pressure on a single-point of the registry.

Dragonfly supports both **mirror** mode and HTTP **proxy** mode to boost the containers startup. It is suggested to use Dragonfly mirror mode. To integrate with Dragonfly in the mirror mode, please provide registry mirror in nydusd's json configuration file in section `device.backend.mirrors`

```json
{
  "mirrors": [
    {
      "host": "http://127.0.0.1:65001",
      "headers": "https://index.docker.io/v1/",
      "auth_through": false
    }
  ]
}
```

`auth_through=false` means nydusd's authentication request will directly go to original registry rather than relayed by Dragonfly.

## Community

Nydus aims to form a **vendor-neutral opensource** image distribution solution to all communities.
Questions, bug reports, technical discussion, feature requests and contribution are always welcomed!

We're very pleased to hear your use cases any time.
Feel free to reach/join us via Slack and/or Dingtalk.

- **Slack:** [Nydus Workspace](https://join.slack.com/t/nydusimageservice/shared_invite/zt-pz4qvl4y-WIh4itPNILGhPS8JqdFm_w)

- **Twitter:** [@dragonfly_oss](https://twitter.com/dragonfly_oss)

- **Dingtalk:** [34971767](https://qr.dingtalk.com/action/joingroup?code=v1,k1,ioWGzuDZEIO10Bf+/ohz4RcQqAkW0MtOwoG1nbbMxQg=&_dt_no_comment=1&origin=11)

<img src="https://github.com/dragonflyoss/image-service/blob/master/misc/dingtalk.jpg" width="250" height="300"/>

- **Technical Meeting:** Every Wednesday at 06:00 UTC (Beijing, Shanghai 14:00), please see our [HackMD](https://hackmd.io/@Nydus/Bk8u2X0p9) page for more information.

## License

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fcontainerd%2Fnydus-snapshotter.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fcontainerd%2Fnydus-snapshotter?ref=badge_large)
