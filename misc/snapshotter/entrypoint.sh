#! /bin/bash

CONTAINERD_ROOT="${CONTAINERD_ROOT:-/var/lib/containerd/}"

set -eu
BACKEND_TYPE="${BACKEND_TYPE:-config}"
if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
	    --log-level trace \
	    --nydusd-path /usr/local/bin/nydusd \
	    --config-path /etc/nydus/${BACKEND_TYPE}.json \
	    --root ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus \
	    --address ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock \
	    --enable-nydus-overlayfs \
	    --daemon-mode shared \
			--enable-stargz \
	    --log-to-stdout
fi

exec $@
