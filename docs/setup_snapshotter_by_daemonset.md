# Setup Nydus Snapshotter by DaemonSet

This document will guide you through the simple steps of setting up and cleaning up the nydus snapshotter in a kubernetes cluster that runs on the host.

## Steps for Setting up Nydus Snapshotter 

To begin, let's clone the Nydus Snapshotter repository.

```bash
git clone https://github.com/containerd/nydus-snapshotter
cd nydus-snapshotter
```

We can build the docker image locally. (optional)
```bash
$ export NYDUS_VER=$(curl -s "https://api.github.com/repos/dragonflyoss/nydus/releases/latest" | jq -r .tag_name)
$ make  # build snapshotter binaries
$ cp bin/* misc/snapshotter/
$ pushd misc/snapshotter/
$ docker build --build-arg NYDUS_VER="${NYDUS_VER}" -t ghcr.io/containerd/nydus-snapshotter:latest .
$ popd
```
**NOTE:** By default, the nydus snapshotter would use the latest release nydus version. If you want to use a specific version, you can set `NYDUS_VER` on your side.

Next, we can configure access control for nydus snapshotter.
```bash
kubectl apply -f misc/snapshotter/nydus-snapshotter-rbac.yaml
```

Afterward, we can deploy a DaemonSet for nydus snapshotter, according to the kubernetes flavour you're using.

```bash
# Vanilla kubernetes
kubectl apply -f misc/snapshotter/base/nydus-snapshotter.yaml
```

```bash
# k3s
kubectl apply -k misc/snapshotter/overlays/k3s/
```

```bash
# rke2
kubectl apply -k misc/snapshotter/overlays/rke2/
```

Then, we can confirm that nydus snapshotter is running through the DaemonSet.
```bash
$ kubectl get pods -n nydus-system 
NAME                      READY   STATUS    RESTARTS   AGE
nydus-snapshotter-26rf7   1/1     Running   0          18s
```

Finally, we can view the logs in the pod.
```bash
$ kubectl logs nydus-snapshotter-26rf7 -n nydus-system
install nydus snapshotter artifacts
there is no proxy plugin!
Created symlink /etc/systemd/system/multi-user.target.wants/nydus-snapshotter.service → /etc/systemd/system/nydus-snapshotter.service.
```

And we can see the nydus snapshotter service on the host.
```bash
$ systemctl status nydus-snapshotter
● nydus-snapshotter.service - nydus snapshotter
     Loaded: loaded (/etc/systemd/system/nydus-snapshotter.service; enabled; vendor preset: enabled)
    Drop-In: /etc/systemd/system/nydus-snapshotter.service.d
             └─proxy.conf
     Active: active (running) since Wed 2024-01-17 16:14:22 UTC; 56s ago
   Main PID: 1100169 (containerd-nydu)
      Tasks: 11 (limit: 96376)
     Memory: 8.6M
        CPU: 35ms
     CGroup: /system.slice/nydus-snapshotter.service
             └─1100169 /opt/nydus/bin/containerd-nydus-grpc --config /etc/nydus/config.toml

Jan 17 16:14:22 worker systemd[1]: Started nydus snapshotter.
Jan 17 16:14:22 worker containerd-nydus-grpc[1100169]: time="2024-01-17T16:14:22.998798369Z" level=info msg="Start nydus-snapshotter. Version: v0.7.0-308-g106a6cb, PID: 1100169, FsDriver: fusedev, DaemonMode: dedicated"
Jan 17 16:14:23 worker containerd-nydus-grpc[1100169]: time="2024-01-17T16:14:23.000186538Z" level=info msg="Run daemons monitor..."
```

**NOTE:** By default, the nydus snapshotter operates as a systemd service. If you prefer to run nydus snapshotter as a standalone process, you can set `ENABLE_SYSTEMD_SERVICE` to `false` in `nydus-snapshotter.yaml`.

## Steps for Cleaning up Nydus Snapshotter 

We use `preStop`` hook in the DaemonSet to uninstall nydus snapshotter and roll back the containerd configuration.

```bash
# Vanilla kubernetes
$ kubectl delete -f misc/snapshotter/base/nydus-snapshotter.yaml 
```

```bash
# k3s
$ kubectl delete -k misc/snapshotter/overlays/k3s/ 
```

```bash
# rke2
$ kubectl delete -k misc/snapshotter/overlays/rke2/
```

```bash
$ kubectl delete -f misc/snapshotter/nydus-snapshotter-rbac.yaml 
$ systemd restart containerd.service
```

## Customized Setup

As we know, nydus snapshotter supports four filesystem drivers (fs_driver): `fusedev`, `fscache`, `blockdev`, `proxy`. Within the container image, we have included configurations for these snapshotter drivers, as well as the corresponding nydusd configurations. By default, the fusedev driver is enabled in the nydus snapshotter, using the snapshotter configuration [`config-fusedev.toml`](../misc/snapshotter/config-fusedev.toml) and the nydusd configuration [`nydusd-config.fusedev.json`](../misc/snapshotter/nydusd-config.fusedev.json).

### Other filesystem driver with related default configuration

If we want to setup the nydus snapshotter with the default configuration for different fs_driver (such as `proxy`), we can modify the values in the `Configmap` in `nydus-snapshotter.yaml`:
```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: nydus-snapshotter-configs
  labels:
    app: nydus-snapshotter
  namespace: nydus-snapshotter
data:
  FS_DRIVER: "proxy"
  NYDUSD_DAEMON_MODE: "none"
```

Then we can run the nydus snapshotter enabling `proxy` `fs_driver` with the snapshotter configuration [`config-proxy.toml`](../misc/snapshotter/config-proxy.toml).

**NOTE:** The fs_driver (`blockdev` and `proxy`) do not need nydusd, so they do not need nydusd config.

### Same filesystem with different snapshotter configuration and different nydusd configuration

If we want to setup the nydus snapshotter for the same fs_driver (such as `fusedev`) with different snapshotter configuration and different nydusd configuration, we can enable `ENABLE_CONFIG_FROM_VOLUME` and add the snapshotter configuration [`config.toml`](../misc/snapshotter/config.toml) and nydusd configuration [`nydusd-config.json`](../misc/snapshotter/nydusd-config.fusedev.json) in the `Configmap` in `nydus-snapshotter.yaml`:

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: nydus-snapshotter-configs
  labels:
    app: nydus-snapshotter
  namespace: nydus-snapshotter
data:
  ENABLE_CONFIG_FROM_VOLUME: "true"

  config.toml: |-
    # The snapshotter config content copied here

  nydusd-config.json: |-
    # The nydusd config content copied here
```

**NOTE:** We need to set `nydusd_config` to `/etc/nydus/nydusd-config.json` in the `config.toml`, so that snapshotter can find the nydusd configuration from configmap.

### Customized Options

| Options                             | Type   | Default                               | Comment                                                                                                                                         |
| ----------------------------------- | ------ | ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| FS_DRIVER                           | string | "fusedev"                             | the filesystem driver of snapshotter                                                                                                            |
| ENABLE_CONFIG_FROM_VOLUME           | bool   | false                                 | enabling to use the configurations from volume                                                                                                  |
| ENABLE_RUNTIME_SPECIFIC_SNAPSHOTTER | bool   | false                                 | enabling to skip to set `plugins."io.containerd.grpc.v1.cri".containerd` to `nydus` for runtime specific snapshotter feature in containerd 1.7+ |
| ENABLE_SYSTEMD_SERVICE              | bool   | true                                  | enabling to run nydus snapshotter as a systemd service                                                                                          |
