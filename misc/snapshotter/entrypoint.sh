#! /bin/bash

CONTAINERD_ROOT="${CONTAINERD_ROOT:-/var/lib/containerd/}"

set -eu

if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
	    --log-level trace \
	    --nydusd-path /usr/local/bin/nydusd \
	    --config-path /etc/nydus/config.json \
	    --root ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus \
	    --address ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock \
	    --enable-nydus-overlayfs \
	    --daemon-mode shared \
	    --log-to-stdout
fi

exec $@
