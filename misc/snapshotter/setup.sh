#!/bin/bash

set -e

ret=$(yq -oj '.proxy_plugins.nydus' /etc/containerd/config.toml)

need_start_containerd=false

if [[ ${ret} != "null" ]]; then
    echo "Nydus snapshotter has been configured!"
else
  echo "Added nydus-snapshotter to Containerd's configuration file"

  echo '
# Added by nydus-snapshotter automaticaly, nydus-snapshotter will not remove the table even being uninstalled!
[proxy_plugins.nydus]
  type = "snapshot"
  # The "address" field specifies through which socket snapshotter and containerd communicate.
  address = "/run/containerd-nydus/containerd-nydus-grpc.sock"' >> /etc/containerd/config.toml
    
  need_start_containerd=true
fi

ret=$(yq -oj '.plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc-nydus' /etc/containerd/config.toml)
if [[ ${ret} != "null" ]]; then
    echo "Runtime handler has been configured!"
else
    echo "Added runc-nydus runtime handler to Containerd's configuration file"
    
    echo '
# Added by nydus-snapshotter automaticaly, nydus-snapshotter will not remove the table even being uninstalled!
[plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc-nydus]
  runtime_type = "io.containerd.runc.v2"
  snapshotter = "nydus"
  [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runc-nydus.options]
    NoPivotRoot = false
    NoNewKeyring = false
    SystemdCgroup = false' >> /etc/containerd/config.toml

   need_start_containerd=true
fi

if [[ $need_start_containerd == "true" ]]; then
  echo "Restart Containerd service" 
  chroot /proc/1/root bash -c "systemctl restart containerd"
else
  echo "No need to restart containerd on host"
fi

pushd /nydus-static
install -D -m 755 nydusd nydusctl nydus-image /opt/nydus/bin
popd

mkdir -p /etc/nydus && cp /nydus-static/configs/nydusd-config.json /etc/nydus/nydusd-config.json


printf "Executing nydus-snapshotter...\n\n"
exec ./containerd-nydus-grpc --nydusd-config /etc/nydus/nydusd-config.json --nydusd /opt/nydus/bin/nydusd --nydus-image /opt/nydus/bin/nydus-image --log-to-stdout "$@"