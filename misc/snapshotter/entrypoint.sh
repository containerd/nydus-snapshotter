#! /bin/bash

CONTAINERD_ROOT="${CONTAINERD_ROOT:-/var/lib/containerd/}"

set -eu
MODE="config"
if [ "$#" -eq 0 ]; then
	if [ ! -z ${BACKEND_TYPE} ];then 
		MODE=${BACKEND_TYPE}
	fi
	containerd-nydus-grpc \
	    --log-level trace \
	    --nydusd-path /usr/local/bin/nydusd \
	    --config-path /etc/nydus/${MODE}.json \
	    --root ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus \
	    --address ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock \
	    --enable-nydus-overlayfs \
	    --daemon-mode shared \
	    --log-to-stdout
fi

exec $@
