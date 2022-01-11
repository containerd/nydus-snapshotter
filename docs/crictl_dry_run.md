# Start Container by crictl

## Create crictl Config

The runtime endpoint can be set in the config file. Please refer to [crictl](https://github.com/kubernetes-sigs/cri-tools/blob/master/docs/crictl.md) document for more details.

Compose your `crictl` configuration file named as `crictl.yaml`:

```yml
runtime-endpoint: unix:///run/containerd/containerd.sock
image-endpoint: unix:///run/containerd/containerd.sock
timeout: 10
debug: true
```

Compose a pod configuration which can be named as `pod.yaml`

```yml
metadata:
  attempt: 1
  name: nydus-sandbox
  namespace: default
  uid: hdishd83djaidwnduwk28bcsb
log_directory: /tmp
linux:
  security_context:
    namespace_options:
      network: 2
```

Compose a container configuration which can be named as `container.yaml`

```yml
metadata:
  name: nydus-container
image:
  image: <nydus-image>
command:
  - /bin/sleep
args:
  - 600
log_path: container.1.log
```

Start a container based on nydus image.

```bash
# auth is base64 encoded string from "username:password"
$ crictl --config ./crictl.yaml run --auth <base64 of registry auth> ./container.yaml ./pod.yaml
```
