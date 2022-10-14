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

[[resolver.host."source-registry1.com".mirrors]]
host = "http://127.0.0.1:65001"
insecure = false
[resolver.host."source-registry1.com".mirrors.headers]
"X-Dragonfly-Registry" = "http//source-registry1.com"

[[resolver.host."source-registry1.com".mirrors]]
host = "http://127.0.0.1:65001"
insecure = true
[resolver.host."source-registry1.com".mirrors.headers]
"X-Dragonfly-Registry" = "https//source-registry1.com"
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
