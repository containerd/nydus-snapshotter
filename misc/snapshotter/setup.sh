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

NRI_CONF=/etc/nri/conf.json
NRI_CONF_TMP=/etc/nri/conf.json.tmp

# If the nri conf.json exists, it must be validate which means it can't be empty. So we only append rootfs persister NRI's configuration to the conf list.
if [[ -f ${NRI_CONF} ]]; then
  echo "Update NRI configuration"
  # Delete rootfs-persister conf from list before add the new one.
  jq --arg type rootfs-persister 'del(.plugins[] | select(.type == $type))' ${NRI_CONF} > $NRI_CONF_TMP && mv $NRI_CONF_TMP $NRI_CONF

  jq --argjson pid $$  '.plugins += [{
      "type": "rootfs-persister",
      "conf": {
        "pid": $pid
      }
    }]' ${NRI_CONF} > $NRI_CONF_TMP && mv $NRI_CONF_TMP $NRI_CONF
 
else 
  echo "Create NRI configuration"

  touch ${NRI_CONF}

  echo "{
  \"version\": \"0.1\",
  \"plugins\": [
    {
      \"type\": \"rootfs-persister\",
      \"conf\": {
        \"pid\": $$
      }
    }
  ]
}" > ${NRI_CONF}

fi

install rootfs-persister /opt/nri/bin

mkdir -p /opt/nydus/bin
install nydusd nydus-image nydusctl /opt/nydus/bin


printf "Executing nydus-snapshotter...\n\n"

exec ./containerd-nydus-grpc --log-to-stdout "$@"