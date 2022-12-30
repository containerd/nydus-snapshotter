# How to Configure Nydus

Nydus-snapshotter can receive a toml file as Nudus-snapshotter configuration to startup Nydus-snapshotter
through CLI option `--config-path`.The `--config-path` can also be set to a json file path as nydusd configuration.
An example configuration looks like below.
You can refer to the following template for configuration.

```toml
# Nydus-snapshotter startup parameter
[snapshotter]
address = "/run/containerd-nydus/containerd-nydus-grpc.sock"
config_path = "/etc/nydusd-config.json" 
root_dir  = "/var/lib/containerd-nydus"
cache_dir  = "/var/lib/nydus/cache"
nydusd_binary_path  = "/usr/local/bin/nydusd"
nydus_image_binary_path = "/usr/local/bin/nydus-image"
log_to_stdout = true
log_level = "info"
log_dir = "logs"
gc_period = "24h"
validate_signature = true
public_key_file = "/signing/nydus-image-signing-public.key"
convert_vpc_registry = true
daemon_mode = "multiple"
fs_driver = "fusedev"
sync_remove = true
enable_metrics = true
metrics_file ="metrics"
enable_stargz = true
disable_cache_manager = false
enable_nydus_overlay_fs = true
nydusd_thread_num = 1
cleanup_on_close = false
kubeconfig_path = "$HOME/.kube/config"
enable_kubeconfig_keychain = true
recover_policy = "restart"
enable_cri_keychain = false
image_service_address = ""
```

```json
{
  "device": {
    "backend": {
      "type": "registry",
      "config": {
        "scheme": "https",
        "auth": "<registry auth token>",
        "timeout": 5,
        "connect_timeout": 5,
        "retry_limit": 0
      }
    },
    "cache": {
      "type": "blobcache",
      "config": {
        "work_dir": "/tmp/cache"
      }
    }
  },
  "mode": "direct",
  "digest_validate": false,
  "iostats_files": false,
  "enable_xattr": true,
  "fs_prefetch": {
    "enable": true,
    "threads_count": 6,
    "merging_size": 131072
  }
}
```


## Authentication
As [contianerd#3731](https://github.com/containerd/containerd/issues/3731) discussed, containerd doesn't share credentials with third snapshotters now. Like [stargz snapshotter](https://github.com/containerd/stargz-snapshotter/blob/main/docs/overview.md#authentication), nydus-snapshotter supports 3 main ways to access registries with custom configurations. You can use config file or command line flags to enable them.

The snapshotter will try to get image pull keychain in the following order if such way is enabled:

1. cri request
2. docker config (default enabled)
3. k8s docker config secret

### dockerconfig-based authentication

By default, the snapshotter tries to get creds from `$DOCKER_CONFIG` or `~/.docker/config.json`.
Following example enables nydus-snapshotter to access to private registries using `docker login` command. 

```console
# docker login
(Enter username and password)
# crictl pull --creds USERNAME[:PASSWORD] docker.io/<your-repository>/ubuntu:22.04
(Here the creds is only used by containerd)
```

### CRI-based authentication

Following configuration enables nydus-snapshotter to pull private images via CRI requests.

```toml
enable_cri_keychain = true
image_service_address = "/run/containerd/containerd.sock"
```

The equivalent command line flags are `--enable-image-proxy-keychain --image-service-address "/run/containerd/containerd.sock"`

The snapshotter works as a proxy of CRI Image Service and exposes CRI Image Service API on the snapshotter's unix socket (i.e. `/run/containerd/containerd-nydus-grpc.sock`). The snapshotter acquires registry creds by scanning requests.
The `image_service_address` is the original image service socket. It can be omitted and the default value will be the containerd's default socket path `/run/containerd/containerd.sock`.


You **must** specify `--image-service-endpoint=unix:///run/containerd/containerd-nydus-grpc.sock` option to kubelet when using Kubernetes. Or specify `image-endpoint: "unix:////run/containerd/containerd-nydus-grpc.sock"` in `crictl.yaml` when using crictl.

### kubeconfig-based authentication

This is another way to enable lazy pulling of private images on Kubernetes, Nydus snapshotter will start a goroutine to listen on secrets (type = `kubernetes.io/dockerconfigjson`) for private registries.

#### use kubeconfig file

Following configuration enables nydus-snapshotter to access to private registries using kubernetes secrets in the cluster using kubeconfig files.

```toml
enable_kubeconfig_keychain = true
kubeconfig_path = "$HOME/.kube/config"
```
The equivalent command line flags are `--enable-kubeconfig-keychain --kubeconfig-path "$HOME/.kube/config"`

If no `kubeconfig_path` is specified, snapshotter searches kubeconfig files from `$KUBECONFIG` or `~/.kube/config`.

Please note that kubeconfig-based authentication requires additional privilege (i.e. kubeconfig to list/watch secrets) to the node.
And this doesn't work if kubelet retrieve creds from somewhere not API server (e.g. [credential provider](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/)).

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

If you have logined into a private registry, you can create a secret from the config file:

```bash
$ kubectl create --namespace nydus-system secret generic regcred \
    --from-file=.dockerconfigjson=$HOME/.docker/config.json \
    --type=kubernetes.io/dockerconfigjson
```

The Nydus snapshotter will get the new secret and parse the authorization. If your new Pod uses a private registry, then this authentication information will be used to pull the image from the private registry.
