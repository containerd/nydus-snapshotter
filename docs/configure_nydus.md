# How to Configure Nydus

Nydus-snapshotter receives a json file as nydusd configuration through CLI option `--config-path`.
An example configuration looks like below.
You can find registry auth from local docker configuration.

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
