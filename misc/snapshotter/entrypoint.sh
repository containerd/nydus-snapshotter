#! /bin/bash

CONTAINERD_ROOT="${CONTAINERD_ROOT:-/var/lib/containerd/}"

set -eu
BACKEND_TYPE="${BACKEND_TYPE:-config}"
NYDUSD_DAEMON_MODE="${NYDUSD_DAEMON_MODE:-shared}"

if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
		--log-level info \
		--nydusd-path /usr/local/bin/nydusd \
		--config-path /etc/nydus/${BACKEND_TYPE}.json \
		--root ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus \
		--address ${CONTAINERD_ROOT}/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock \
		--enable-nydus-overlayfs \
		--daemon-mode ${NYDUSD_DAEMON_MODE} \
		--enable-stargz \
		--log-to-stdout
fi

exec $@
