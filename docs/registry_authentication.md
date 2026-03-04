# Registry Authentication

As [containerd#3731](https://github.com/containerd/containerd/issues/3731) discussed, containerd doesn't share credentials with third-party snapshotters. Like [stargz snapshotter](https://github.com/containerd/stargz-snapshotter/blob/main/docs/overview.md#authentication), nydus-snapshotter supports multiple ways to access private registries with custom configurations.

Credentials are looked up in the following priority order:

1. Snapshot labels (username and password)
2. CRI request interception
3. Docker config (enabled by default)
4. Kubelet credential provider plugins
5. Kubernetes docker config secrets

## Docker config

By default, the snapshotter reads credentials from `$DOCKER_CONFIG` or `~/.docker/config.json`.

```console
# docker login
(Enter username and password)
# crictl pull --creds USERNAME[:PASSWORD] docker.io/<your-repository>/ubuntu:22.04
(Here the credential is only used by containerd)
```

## CRI-based authentication

The following configuration enables nydus-snapshotter to pull private images via CRI requests.

```toml
[remote.auth]
# Fetch the private registry auth as CRI image service proxy
enable_cri_keychain = true
image_service_address = "/run/containerd/containerd.sock"
```

The snapshotter acts as a proxy of the CRI Image Service, exposing the CRI Image Service API on the snapshotter's unix socket (i.e. `/run/containerd/containerd-nydus-grpc.sock`). It acquires registry credentials by scanning requests. `image_service_address` defaults to `/run/containerd/containerd.sock` if omitted.

You **must** specify `--image-service-endpoint=unix:///run/containerd-nydus/containerd-nydus-grpc.sock` to kubelet when using Kubernetes, or set `image-endpoint: "unix:////run/containerd-nydus/containerd-nydus-grpc.sock"` in `crictl.yaml` when using `crictl`.

## Kubeconfig-based authentication

Nydus snapshotter can watch Kubernetes secrets (type `kubernetes.io/dockerconfigjson`) for private registry credentials.

### Using a kubeconfig file

```toml
[remote.auth]
enable_kubeconfig_keychain = true
kubeconfig_path = "/path/to/.kubeconfig"
```

If `kubeconfig_path` is omitted, the snapshotter searches `$KUBECONFIG` or `~/.kube/config`.

> **Note:** This requires additional privilege (a kubeconfig with permission to list/watch secrets) on the node. It does not work if kubelet retrieves credentials outside the API server (e.g. via a [credential provider](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/)). For that use case, see the [kubelet credential provider](#kubelet-credential-provider) section.

### Using a ServiceAccount

If nydus-snapshotter runs inside a Kubernetes cluster you can use a ServiceAccount instead of a kubeconfig:

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

Then reference the ServiceAccount in the nydus-snapshotter Pod spec:

```yaml
spec:
  serviceAccountName: nydus-snapshotter-sa
```

### Creating secrets

If you have logged into a private registry, create a secret from the local config file:

```bash
kubectl create --namespace nydus-system secret generic regcred \
    --from-file=.dockerconfigjson=$HOME/.docker/config.json \
    --type=kubernetes.io/dockerconfigjson
```

The snapshotter will pick up the secret and use it for subsequent image pulls from the matched registry.

## Kubelet credential provider

Nydus snapshotter supports [Kubernetes kubelet credential provider plugins](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/), which allow dynamic credential retrieval from external sources such as cloud provider identity services or secrets managers.

> **Note:** This implementation supports **exec-based credential provider plugins** (GA since [Kubernetes v1.26](https://kubernetes.io/blog/2022/12/22/kubelet-credential-providers/)) but does **not** support the [service account token integration](https://kubernetes.io/blog/2025/05/07/kubernetes-v1-33-wi-for-image-pulls/) introduced in Kubernetes v1.33+. Plugins must retrieve credentials via other means (instance metadata, environment variables, local credential files, etc.).

This authentication method is well-suited for:

- Frequently rotated credentials managed by external systems
- Cloud provider-managed registries (ECR, GCR, ACR) using instance or workload identity
- Enterprise secrets management integrations

### Configuration

```toml
[remote.auth]
enable_kubelet_credential_providers = true
credential_provider_config = "/etc/nydus/credential-provider-config.yaml"
credential_provider_bin_dir = "/usr/local/bin/credential-providers"
```

| Parameter | Description |
|---|---|
| `enable_kubelet_credential_providers` | Enable kubelet credential provider support (default: `false`) |
| `credential_provider_config` | Path to the credential provider configuration file |
| `credential_provider_bin_dir` | Directory containing credential provider plugin binaries |

### Credential provider configuration file

Follow the [Kubernetes spec](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1/#kubelet-config-k8s-io-v1-CredentialProviderConfig):

```yaml
apiVersion: kubelet.config.k8s.io/v1
kind: CredentialProviderConfig
providers:
  - name: ecr-credential-provider
    apiVersion: credentialprovider.kubelet.k8s.io/v1
    matchImages:
      - "*.dkr.ecr.*.amazonaws.com"
    defaultCacheDuration: "12h"
    args:
      - get-credentials

  - name: gcr-credential-provider
    apiVersion: credentialprovider.kubelet.k8s.io/v1
    matchImages:
      - "gcr.io"
      - "*.gcr.io"
    defaultCacheDuration: "1h"
```

### Plugin installation

1. Place credential provider binaries in the `credential_provider_bin_dir` directory and ensure they are executable.
2. Configure `matchImages` patterns to match your registry URLs. Wildcards are supported (e.g. `*.dkr.ecr.*.amazonaws.com`).

For AWS ECR, use the official [ECR credential provider](https://github.com/kubernetes/cloud-provider-aws/tree/master/cmd/ecr-credential-provider):

```bash
mkdir -p /usr/local/bin/credential-providers
wget https://artifacts.k8s.io/binaries/cloud-provider-aws/v1.30.0/linux/amd64/ecr-credential-provider
chmod +x ecr-credential-provider
mv ecr-credential-provider /usr/local/bin/credential-providers/
```

### Plugin protocol

Plugins communicate via stdin/stdout using JSON.

**Request:**

```json
{
  "kind": "CredentialProviderRequest",
  "apiVersion": "credentialprovider.kubelet.k8s.io/v1",
  "image": "123456789.dkr.ecr.us-east-1.amazonaws.com/my-image:latest"
}
```

**Response:**

```json
{
  "kind": "CredentialProviderResponse",
  "apiVersion": "credentialprovider.kubelet.k8s.io/v1",
  "cacheKeyType": "Image",
  "cacheDuration": "12h",
  "auth": {
    "123456789.dkr.ecr.us-east-1.amazonaws.com": {
      "username": "AWS",
      "password": "<token>"
    }
  }
}
```

Set `auth` to `null` if no credentials are available for the requested image.

### Troubleshooting

- Plugin execution failures are logged but don't halt the authentication chain — the snapshotter tries the next provider.
- Check logs for: `level=warning msg="failed to execute credential provider plugin"`
- Test plugins manually by sending a JSON request on stdin to verify output.

## Credential renewal

For providers that issue short-lived tokens (such as the kubelet credential provider with cloud IAM backends), nydus-snapshotter can automatically renew credentials in the background before they expire.

When enabled, a background goroutine periodically reconciles the set of active RAFS instances against an in-memory credential store, renewing credentials for images currently in use and evicting entries for images that are no longer mounted. On each renewal tick the goroutine re-queries the renewable providers (Docker config, kubelet credential providers, Kubernetes secrets) in priority order.

Only providers that support renewal participate: Docker config, kubelet credential providers, and Kubernetes secret-based providers. CRI-based and label-based credentials are not renewed. The kubelet provider being
expiration aware, it will only renew tokens when they are about to expire.
Entries are evicted when the corresponding RAFS instance is no longer mounted; the credential store itself does not expire entries.

### Configuration

```toml
[remote.auth]
# How often to renew credentials. Set to 0 (the default) to disable.
credential_renewal_interval = "30m"
```

Set `credential_renewal_interval` to at most one third of your token lifetime. This ensures at least two renewal attempts before a token expires, so a single transient failure (network blip, metadata service hiccup) does not cause an auth outage. For example, if ECR tokens are valid for 12 hours, use an interval of 4 hours or less.

> **Future improvement:** The current configuration conflates two concerns into a single interval: how frequently the renewal loop runs, and how early before expiry a token should be renewed. A future version may separate these into a `credential_renewal_check_interval` (the loop cadence, kept short) and a `credential_renewal_lead_time` (how far before expiry to trigger renewal, e.g. 2 hours before a 12-hour token expires). This would allow fine-grained control without the lifetime/3 approximation.

### Metrics

When credential renewal is enabled, the following Prometheus metrics are exported:

| Metric | Type | Description |
|---|---|---|
| `snapshotter_credential_renewals_total` | Counter | Renewal attempts, labeled by `image_ref` and `result` (`success` or `failure`) |
| `snapshotter_credential_store_entries` | Gauge | Number of credentials tracked per `image_ref` |

A rising `failure` count in `snapshotter_credential_renewals_total` indicates that a provider is failing to renew credentials and warrants investigation before the current tokens expire.
