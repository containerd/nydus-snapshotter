# Kubernetes User Namespaces

This guide describes how to run and verify Kubernetes user-namespace workloads
(`hostUsers: false`) with `nydus-snapshotter`.

Date validated: **2026-04-15**

## 1. Prerequisites

- Kernel supports idmapped mounts (Linux 5.19+ recommended; newer is better).
- `containerd` version 2.0 (and later) supports user namespaces for containers.
- `containerd` has nydus proxy plugin configured and healthy.
- `nydus-snapshotter` service is running.
- For minikube, use `v1.36.0+`.
- For Kubernetes user-namespace version requirements, refer to the
  [official documentation](https://kubernetes.io/docs/concepts/workloads/pods/user-namespaces/).

Verify snapshotter plugin:

```bash
sudo ctr plugins ls | grep nydus
```

## 2. Containerd Configuration

In `/etc/containerd/config.toml`:

```toml
[proxy_plugins]
  [proxy_plugins.nydus]
    type = "snapshot"
    address = "/run/containerd-nydus/containerd-nydus-grpc.sock"
    capabilities = ["remap-ids"]
```

Then restart:

```bash
sudo systemctl restart containerd
```

## 3. Enable Kubernetes Userns Feature Gate

`hostUsers: false` is only honored when `UserNamespacesSupport=true`.

For minikube:

```bash
sudo minikube start \
  --driver=none \
  --container-runtime=containerd \
  --extra-config=apiserver.feature-gates=UserNamespacesSupport=true \
  --extra-config=kubelet.feature-gates=UserNamespacesSupport=true
```

If this gate is not enabled, pod may still run, but it is not real userns remap.

## 4. RuntimeClass for Nydus

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: nydus-runc
handler: nydus-runc
```

Apply:

```bash
sudo KUBECONFIG=/etc/kubernetes/admin.conf kubectl apply -f runtimeclass-nydus.yaml
```

Make sure your containerd runtime handler exists:

```toml
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.nydus-runc]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "nydus"
```

## 5. Example Pod (`hostUsers: false`)

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nydus-idmap-demo
spec:
  hostUsers: false
  runtimeClassName: nydus-runc
  restartPolicy: Never
  containers:
  - name: app
    image: localhost:5000/nydus/busybox:minikube-v136
    command: ["sh","-lc","echo idmap-ok; sleep 120"]
```

## 6. How to Verify It Is Real ID Mapping

1. Confirm pod spec keeps `hostUsers: false`:

```bash
sudo KUBECONFIG=/etc/kubernetes/admin.conf kubectl get pod nydus-idmap-demo -o jsonpath='{.spec.hostUsers}'
```

2. Get pod sandbox and inspect UID/GID mappings:

```bash
CID=$(sudo KUBECONFIG=/etc/kubernetes/admin.conf kubectl get pod nydus-idmap-demo -o jsonpath='{.status.containerStatuses[0].containerID}' | sed 's#containerd://##')
SID=$(sudo crictl ps -a -o json | jq -r ".containers[] | select(.id==\"$CID\") | .podSandboxId")
sudo crictl inspectp -o json "$SID" | jq '.info.runtimeSpec.linux.uidMappings, .info.runtimeSpec.linux.gidMappings'
```

Expected:

- both mappings are non-empty arrays
- each mapping should contain `containerID/containerId: 0` and `size/length: 65536` in typical setup

3. Optional check: no compatibility fallback keys:

```bash
sudo ctr -n k8s.io snapshots --snapshotter nydus ls | grep remap || echo no-remap-keys
```

## 7. Commit/Use Validation (Optional but Recommended)

Commit from a running idmapped pod container and re-run:

```bash
CID=$(sudo KUBECONFIG=/etc/kubernetes/admin.conf kubectl get pod nydus-idmap-demo -o jsonpath='{.status.containerStatuses[0].containerID}' | sed 's#containerd://##')
sudo nerdctl --address /run/containerd/containerd.sock --namespace k8s.io commit "$CID" localhost:5000/nydus/commit-from-idmap:test
sudo nerdctl --address /run/containerd/containerd.sock --namespace k8s.io run --rm --snapshotter nydus --insecure-registry localhost:5000/nydus/commit-from-idmap:test sh -lc 'echo commit-use-ok'
```

If this succeeds, commit artifact is usable through nydus snapshotter path.

## 8. Common Pitfalls

1. `hostUsers: false` appears in client annotation but not in pod spec:
   - `UserNamespacesSupport` is not enabled.
2. Pod runs but mappings are null:
   - still not in real idmap path; check feature gate and runtime handler.
3. `mknod ... permission denied` with idmap/remap labels:
   - verify `capabilities = ["remap-ids"]`.
4. `input/output error` reading rootfs files on nydus-native image:
   - check nydusd backend type matches image storage backend (for example `registry` vs `s3`).
