# User self-defined nydus image files prefetch
In the on-demand loading mechanism, the dynamic loading of files or dependencies within the 
container during runtime may generate considerable network IO, affecting performance. 
The `--prefetch-patterns` parameter provided by `nydusify` enables you to specify a prefetch list 
when converting an image to the Nydus format. This list is utilized during container startup to fetch 
files according to the specified patterns. However, this method may lack flexibility as different 
services using the same image might require access to distinct files.

To improve the flexibility of nydus image files prefetch, for the k8s scenario, we can specify a prefetch files list when create a nydus daemon. The prefetch files list is user self-defined. Nydus-snapshotter has implemented a containerd NRI plugin to transmit the path of prefetch files list to nydus-snapshotter. The prefetch plugin requires NRI 2.0, which is available in containerd (>=v1.7.0). The prefetch plugin subscribes pod creation event, obtains the URL address containing the content of the files need to be prefetched, and forwards it to `nydus-snapshotter`. The `nydus-snapshotter` reads the data through the URL and stores it locally. Then when `nydusd` starts, it will pull the files defined in the prefetch files list through lazy loading. This allows the pull of the prefetch files to be done during container creation rather than image convert, improving the flexibility of file prefetching.

## Requirements

- [NRI 2.0](https://github.com/containerd/nri): Which has been integrated into containerd since [v1.7.0](https://github.com/containerd/containerd/tree/v1.7.0).

## Workflow

1. Add information such as image reference and URL address containing prefetch files to annotations in pod configuration file.
2. Run the prefetch plugin to monitor RunPodSandbox events.
3. The prefetch plugin fetches image reference and URL and forwards them to nydus-snapshotter.
4. Nydus-snapshotter specifies the prefetch list when starting nydus daemon.
5. Nydusd completes the mounting of the nydus image.

## Modify configuration file 

Modify containerd's toml configuration file to enable NRI.
```console
sudo tee -a /etc/containerd/config.toml <<- EOF
[plugins."io.containerd.nri.v1.nri"]
    config_file = "/etc/nri/nri.conf"
    disable = false
    plugin_path = "/opt/nri/plugins"
    socket_path = "/var/run/nri.sock"
EOF
```
Containerd will load all NRI plugins in the `plugin_path` directory on startup. If you want to start an NRI plugin manually, please add the following configuration to allow other NRI plugins to connect via `socket_path`.
```console
sudo tee /etc/nri/nri.conf <<- EOF
disableConnections: false
EOF
```

If you want to start the plugin using `pre-connection` mode. You need to write a configuration file and place the plugin's binary file and configuration file in the correct directories. Here is an example of configuration file:
```console
sudo tee prefetchfiles-nri-plugin.conf <<- EOF
# UNIX domain socket address for connection to the nydus-snapshotter API
socket_address = "/run/containerd-nydus/system.sock"
EOF
```

Then install the prefetchfiles-nri-plugin, invoke below command:
```console
make prefetch && make install-prefetch
```

Restart the containerd service.
```console
sudo systemctl restart containerd
```

When manually starting the prefetch NRI plugin, the socket address can be modified through the command line parameter `socket-addr`. 

After start the prefetch plugin, it will monitor pod creation events. Note that NRI plugin can only be called from containerd/CRI. So creation a pod using crictl as below.
```console
sudo tee pod.yaml <<- EOF
kind: pod
metadata:
  name: wordpress-sandbox
  namespace: default
  attempt: 1
  uid: hdishd83djaidwnduwk28bcsb
log_directory: /tmp
annotations:
 containerd.io/nydus-prefetch: |
    [
        {
            "image": "ghcr.io/dragonflyoss/image-service/wordpress:nydus-nightly-v6", 
            "prefetch": "http://example.com/api/v1/resource/wordpress"
        }
    ]

linux: {}

EOF

crictl runp pod.yaml
```

The list of files to be prefetched is written in a URL, and `nydus-snapshotter` will read the prefetch list based on the URL address and transfer it to a local file. The specific content of the prefetch files can be customized by the user.
`http://example.com/api/v1/resource/wordpress` is just an example of URL address, which needs to be replaced with a real URL during actual operation. The following is an example of some prefetch files in URL:
```console
/usr/bin/env
/lib/x86_64-linux-gnu/ld-2.31.so
/etc/ld.so.cache
/lib/x86_64-linux-gnu/libc-2.31.so
/bin/bash
/lib/x86_64-linux-gnu/libtinfo.so.6.2
/lib/x86_64-linux-gnu/libdl-2.31.so
/etc/nsswitch.conf
```

Note that the naming of keys in annotations is fixed, and the values in annotations are user self-defined.
After creating a pod, `nydus-snapshotter` will store the image references and paths of the prefetch list.

## Nydus-snapshotter starts nydusd
Nydusd daemon will start when the container is created. We start a container like below.
```console
sudo tee wordpress.yaml <<- EOF
metadata:
  name: wordpress
image:
  image: ghcr.io/dragonflyoss/image-service/wordpress:nydus-nightly-v6
log_path: wordpress.0.log
linux: {}
EOF

crictl pull ghcr.io/dragonflyoss/image-service/wordpress:nydus-nightly-v6
crictl create <pod_id> wordpress.yaml pod.yaml
```

Then nydus-snapshotter will start a daemon by command as follows.
```editorconfig
nydusd command: /usr/local/bin/nydusd fuse --thread-num 4 --config /var/lib/containerd-nydus/config/cla8jmoipcbs3ljb264g/config.json --bootstrap /var/lib/containerd-nydus/snapshots/23/fs/image/image.boot --mountpoint /var/lib/containerd-nydus/snapshots/23/mnt --apisock /var/lib/containerd-nydus/socket/cla8jmoipcbs3ljb264g/api.sock --log-level info --log-rotation-size 100 --prefetch-files /var/lib/containerd-nydus/prefetch/cla8jmoipcbs3ljb264g/prefetchList
 ```

According to the parameter `--prefetch-files`, we have implemented file prefetching through lazy loading. 
