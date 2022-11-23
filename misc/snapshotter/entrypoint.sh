#! /bin/bash

NYDUS_LIB="${NYDUS_LIB:-/var/lib/containerd-nydus}"
NYDUS_RUN="${NYDUS_RUN:-/run/containerd-nydus}"
ENABLE_NYDUS_OVERLAY="${ENABLE_NYDUS_OVERLAY:-true}"
ENABLE_METRICS="${ENABLE_METRICS:-false}"
LEVEL="${LEVEL:-info}"

set -eu
BACKEND_TYPE="${BACKEND_TYPE:-config}"
NYDUSD_DAEMON_MODE="${NYDUSD_DAEMON_MODE:-shared}"

if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
		--nydusd-path /usr/local/bin/nydusd \
		--config-path /etc/nydus/${BACKEND_TYPE}.json \
		--root ${NYDUS_LIB} \
		--address ${NYDUS_RUN}/containerd-nydus-grpc.sock \
		--log-level ${LEVEL} \
		--enable-metrics ${ENABLE_METRICS} \
		--enable-nydus-overlayfs ${ENABLE_NYDUS_OVERLAY} \
		--daemon-mode ${NYDUSD_DAEMON_MODE} \
		--enable-stargz \
		--log-to-stdout
fi

exec $@
