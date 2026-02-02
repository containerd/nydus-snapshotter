# Configure Nydus-snapshotter

Nydus-snapshotter can receive a toml file as its configurations to start providing image service through CLI parameter `--config`. An example configuration file can be found [here](../misc/snapshotter/config.toml). Besides nydus-snapshotter's configuration, `nydusd`'s configuration has to be provided to nydus-snapshotter too. Nydusd is started by nydus-snapshotter and it is configured by the provided json configuration file. A minimal configuration file can be found [here](../misc/snapshotter/nydusd-config.fusedev.json)

## Authentication

As [containerd#3731](https://github.com/containerd/containerd/issues/3731) discussed, containerd doesn't share credentials with third snapshotters now. Like [stargz snapshotter](https://github.com/containerd/stargz-snapshotter/blob/main/docs/overview.md#authentication), nydus-snapshotter supports multiple ways to access registries with custom configurations. You can use configuration file to enable them.

The snapshotter will try to get image pull keychain in the following order if such way is enabled:

1. snapshot labels (username and password)
2. intercept CRI request and extract private registry auth
3. docker config (default enabled)
4. kubelet credential provider plugins
5. k8s docker config secret

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
And this doesn't work if `kubelet` retrieves credentials from somewhere not API server (e.g. [credential provider](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/)). For that use case, see the kubelet credential provider section below.

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

### kubelet credential provider

Nydus snapshotter supports [Kubernetes kubelet credential provider plugins](https://kubernetes.io/docs/tasks/kubelet-credential-provider/kubelet-credential-provider/), which allow dynamic credential retrieval from external sources like cloud provider identity services, secrets managers, or custom authentication systems.

> **Note:** This implementation supports **exec-based credential provider plugins** (GA since [Kubernetes v1.26](https://kubernetes.io/blog/2022/12/22/kubelet-credential-providers/)) but does **not** support the newer [service account token integration](https://kubernetes.io/blog/2025/05/07/kubernetes-v1-33-wi-for-image-pulls/) feature introduced in Kubernetes v1.33 (alpha) / v1.34 (beta). If you need service account token injection for credential providers, use the standard kubelet credential provider configuration directly with containerd.

This authentication method is particularly useful when:

- Credentials are rotated frequently and managed by external systems
- Using cloud provider-managed registries (ECR, GCR, ACR) with instance/workload identity
- Integrating with enterprise secrets management systems
- Kubelet already uses credential providers for other container runtimes

#### Configuration

Enable kubelet credential providers in your nydus-snapshotter configuration:

```toml
[remote.auth]
enable_kubelet_credential_providers = true
credential_provider_config = "/etc/nydus/credential-provider-config.yaml"
credential_provider_bin_dir = "/usr/local/bin/credential-providers"
```

**Configuration parameters:**

- `enable_kubelet_credential_providers`: Enable kubelet credential provider support (default: `false`)
- `credential_provider_config`: Path to the credential provider configuration file
- `credential_provider_bin_dir`: Directory containing credential provider plugin binaries

#### Credential Provider Configuration

Create a credential provider configuration file following the [Kubernetes spec](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1/#kubelet-config-k8s-io-v1-CredentialProviderConfig):

```yaml
apiVersion: kubelet.config.k8s.io/v1
kind: CredentialProviderConfig
providers:
  # Example: AWS ECR credential provider
  - name: ecr-credential-provider
    apiVersion: credentialprovider.kubelet.k8s.io/v1
    matchImages:
      - "*.dkr.ecr.*.amazonaws.com"
      - "*.dkr.ecr.*.amazonaws.com.cn"
      - "*.dkr.ecr-fips.*.amazonaws.com"
    defaultCacheDuration: "12h"
    args:
      - get-credentials

  # Example: GCP GCR credential provider
  - name: gcr-credential-provider
    apiVersion: credentialprovider.kubelet.k8s.io/v1
    matchImages:
      - "gcr.io"
      - "*.gcr.io"
    defaultCacheDuration: "1h"

  # Example: Custom credential provider
  - name: custom-provider
    apiVersion: credentialprovider.kubelet.k8s.io/v1
    matchImages:
      - "private-registry.example.com"
      - "*.private-registry.example.com"
    defaultCacheDuration: "30m"
    env:
      - name: CUSTOM_ENV_VAR
        value: "custom-value"
    args:
      - "--region"
      - "us-east-1"
```

#### Plugin Installation

1. **Install credential provider binaries** in the directory specified by `credential_provider_bin_dir`:

```bash
# Example: Installing AWS ECR credential provider
mkdir -p /usr/local/bin/credential-providers
wget https://artifacts.k8s.io/binaries/cloud-provider-aws/v1.30.0/linux/amd64/ecr-credential-provider
chmod +x ecr-credential-provider
mv ecr-credential-provider /usr/local/bin/credential-providers/
```

2. **Ensure plugins are executable** and accessible by the nydus-snapshotter process.

3. **Configure match patterns** in your credential provider config to match your registry URLs. Patterns support wildcards:
   - `*.dkr.ecr.*.amazonaws.com` matches any ECR registry
   - `gcr.io` matches exact host
   - `*.gcr.io` matches any subdomain of gcr.io

#### How It Works

When nydus-snapshotter needs credentials for an image:

1. The snapshotter checks if any configured provider's `matchImages` patterns match the image URL
2. For each matching provider (in order), the snapshotter executes the plugin binary
3. The plugin receives a JSON request on stdin with the image URL (without pod service account token)
4. The plugin returns credentials as a JSON response on stdout
5. The valid credential with the best registry match is used
6. NOT SUPPORTED YET: Credentials are cached according to `defaultCacheDuration`

**Note:** Unlike kubelet's native implementation (v1.33+), nydus-snapshotter does not inject pod service account tokens into the credential provider request. Plugins must retrieve credentials using other methods (instance metadata, environment variables, local credential files, etc.).

#### Plugin Protocol

Credential provider plugins communicate via stdin/stdout using JSON:

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

#### Troubleshooting

- **Plugin execution failures are logged** but don't stop the authentication chain - the snapshotter will try the next provider
- **Check logs** for plugin execution errors: `level=warning msg="failed to execute credential provider plugin"`
- **Verify plugin permissions**: Plugins must be executable (`chmod +x`)
- **Test plugins manually**: Execute plugins with a test request to verify they work
- **Cache duration**: Set appropriate `defaultCacheDuration` based on your credential rotation policy

#### Example: AWS ECR

For AWS ECR, you can use the official [ECR credential provider](https://github.com/kubernetes/cloud-provider-aws/tree/master/cmd/ecr-credential-provider):

```toml
[remote.auth]
enable_kubelet_credential_providers = true
credential_provider_config = "/etc/nydus/ecr-credential-provider.yaml"
credential_provider_bin_dir = "/usr/local/bin"
```

The ECR credential provider uses the instance IAM role to retrieve temporary credentials.

## Metrics

Nydusd records metrics in its own format. The metrics are exported via a HTTP server on top of unix domain socket. Nydus-snapshotter fetches the metrics and convert them in to Prometheus format which is exported via a network address. Nydus-snapshotter by default does not fetch metrics from nydusd. You can enable the nydusd metrics download by assigning a network address to `metrics.address` in nydus-snapshotter's toml [configuration file](../misc/snapshotter/config.toml).

Once this entry is enabled, not only nydusd metrics, but also some information about the nydus-snapshotter
runtime and snapshot related events are exported in Prometheus format as well.

## Diagnose

A system controller can be ran insides nydus-snapshotter.
By setting `system.enable` to `true`,  nydus-snapshotter will start a simple HTTP server on unix domain socket `system.address` path and exports some internal working status to users. The address defaults to `/var/run/containerd-nydus/system.sock`
