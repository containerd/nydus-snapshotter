# Configure Nydus-snapshotter

Nydus-snapshotter can receive a toml file as its configurations to start providing image service through CLI parameter `--config`. An example configuration file can be found [here](../misc/snapshotter/config.toml). Besides nydus-snapshotter's configuration, `nydusd`'s configuration has to be provided to nydus-snapshotter too. Nydusd is started by nydus-snapshotter and it is configured by the provided json configuration file. A minimal configuration file can be found [here](../misc/snapshotter/nydusd-config.fusedev.json)

## Authentication

As [containerd#3731](https://github.com/containerd/containerd/issues/3731) discussed, containerd doesn't share credentials with third snapshotters now. Like [stargz snapshotter](https://github.com/containerd/stargz-snapshotter/blob/main/docs/overview.md#authentication), nydus-snapshotter supports 3 main ways to access registries with custom configurations. You can use configuration file to enable them.

The snapshotter will try to get image pull keychain in the following order if such way is enabled:

1. intercept CRI request and extract private registry auth
2. docker config (default enabled)
3. k8s docker config secret

### dockerconfig-based authentication

By default, the snapshotter tries to get credentials from `$DOCKER_CONFIG` or `~/.docker/config.json`.
Following example enables nydus-snapshotter to access to private registries using `docker login` command.

```console
# docker login
(Enter username and password)
# crictl pull --creds USERNAME[:PASSWORD] docker.io/<your-repository>/ubuntu:22.04
(Here the credential is only used by containerd)
```

### CRI-based authentication

Following configuration enables nydus-snapshotter to pull private images via CRI requests.

```toml

[remote.auth]
# Fetch the private registry auth as CRI image service proxy
enable_cri_keychain = true
image_service_address = "/run/containerd/containerd.sock"
```

The snapshotter works as a proxy of CRI Image Service and exposes CRI Image Service API on the snapshotter's unix socket (i.e. `/run/containerd/containerd-nydus-grpc.sock`). The snapshotter acquires registry creds by scanning requests.
The `image_service_address` is the original image service socket. It can be omitted and the default value will be the containerd's default socket path `/run/containerd/containerd.sock`.

You **must** specify `--image-service-endpoint=unix:///run/containerd-nydus/containerd-nydus-grpc.sock` option to kubelet when using Kubernetes. Or specify `image-endpoint: "unix:////run/containerd-nydus/containerd-nydus-grpc.sock"` in `crictl.yaml` when using `crictl`.

### kubeconfig-based authentication

This is another way to enable lazy pulling of private images on Kubernetes, Nydus snapshotter will start a goroutine to listen on secrets (type = `kubernetes.io/dockerconfigjson`) for private registries.

#### use kubeconfig file

Following configuration enables nydus-snapshotter to access to private registries using kubernetes secrets in the cluster using kubeconfig files.

```toml
[remote.auth]
enable_kubeconfig_keychain = true
kubeconfig_path = "/path/to/.kubeconfig"
```

If no `kubeconfig_path` is specified, snapshotter searches kubeconfig files from `$KUBECONFIG` or `~/.kube/config`.

Please note that kubeconfig-based authentication requires additional privilege (i.e. kubeconfig to list/watch secrets) to the node.
And this doesn't work if `kubelet` retrieve credentials from somewhere not API server (e.g. [credential provider](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/)).

#### use ServiceAccount

If your Nydus snapshotter runs in a Kubernetes cluster and you don't want to use kubeconfig, you can also choose to use ServiceAccount to configure the corresponding permissions for Nydus snapshotter.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nydus-snapshotter-sa
  namespace: nydus-system
---
kind: ClusterRole
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: nydus-snapshotter-role
rules:
- apiGroups:
  - ""
  resources:
  - secrets
  verbs:
  - get
  - list
  - watch
---
kind: ClusterRoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: nydus-snapshotter-role-binding
roleRef:
  kind: ClusterRole
  name: nydus-snapshotter-role
  apiGroup: rbac.authorization.k8s.io
subjects:
- kind: ServiceAccount
  name: nydus-snapshotter-sa
  namespace: nydus-system
```

Then you can set the desired ServiceAccount with the required permissions fro your Nydus snapshotter Pod:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nydus-snapshotter
  namespace: nydus-system
spec:
  containers:
    name: nydus-snapshotter
  serviceAccountName: nydus-snapshotter-sa
  ...
```

#### create secrets

If you have logged into a private registry, you can create a secret from the config file:

```bash
$ kubectl create --namespace nydus-system secret generic regcred \
    --from-file=.dockerconfigjson=$HOME/.docker/config.json \
    --type=kubernetes.io/dockerconfigjson
```

The Nydus snapshotter will get the new secret and parse the authorization. If your new Pod uses a private registry, then this authentication information will be used to pull the image from the private registry.

## Metrics

Nydusd records metrics in its own format. The metrics are exported via a HTTP server on top of unix domain socket. Nydus-snapshotter fetches the metrics and convert them in to Prometheus format which is exported via a network address. Nydus-snapshotter by default does not fetch metrics from nydusd. You can enable the nydusd metrics download by assigning a network address to `metrics.address` in nydus-snapshotter's toml [configuration file](../misc/snapshotter/config.toml).

Once this entry is enabled, not only nydusd metrics, but also some information about the nydus-snapshotter 
runtime and snapshot related events are exported in Prometheus format as well.

## Diagnose

A system controller can be ran insides nydus-snapshotter.
By setting `system.enable` to `true`,  nydus-snapshotter will start a simple HTTP server on unix domain socket `system.address` path and exports some internal working status to users. The address defaults to `/var/run/containerd-nydus/system.sock`
