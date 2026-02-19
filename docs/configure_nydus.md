# Configure Nydus-snapshotter

Nydus-snapshotter can receive a toml file as its configurations to start providing image service through CLI parameter `--config`. An example configuration file can be found [here](../misc/snapshotter/config.toml). Besides nydus-snapshotter's configuration, `nydusd`'s configuration has to be provided to nydus-snapshotter too. Nydusd is started by nydus-snapshotter and it is configured by the provided json configuration file. A minimal configuration file can be found [here](../misc/snapshotter/nydusd-config.fusedev.json)

## Authentication

See [Registry Authentication](registry_authentication.md) for the full reference covering Docker config, CRI, kubeconfig, kubelet credential providers, and automatic credential renewal.

## Metrics

Nydusd records metrics in its own format. The metrics are exported via a HTTP server on top of unix domain socket. Nydus-snapshotter fetches the metrics and convert them in to Prometheus format which is exported via a network address. Nydus-snapshotter by default does not fetch metrics from nydusd. You can enable the nydusd metrics download by assigning a network address to `metrics.address` in nydus-snapshotter's toml [configuration file](../misc/snapshotter/config.toml).

Once this entry is enabled, not only nydusd metrics, but also some information about the nydus-snapshotter
runtime and snapshot related events are exported in Prometheus format as well.

## Diagnose

A system controller can be ran insides nydus-snapshotter.
By setting `system.enable` to `true`,  nydus-snapshotter will start a simple HTTP server on unix domain socket `system.address` path and exports some internal working status to users. The address defaults to `/var/run/containerd-nydus/system.sock`
