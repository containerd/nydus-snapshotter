#! /bin/sh

NYDUS_LIB="${NYDUS_LIB:-/var/lib/containerd-nydus}"
NYDUS_RUN="${NYDUS_RUN:-/run/containerd-nydus}"
LEVEL="${LEVEL:-info}"

set -eu
BACKEND_TYPE="${BACKEND_TYPE:-config}"
NYDUSD_DAEMON_MODE="${NYDUSD_DAEMON_MODE:-multiple}"

if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
		--nydusd-path /usr/local/bin/nydusd \
		--config-path /etc/nydus/${BACKEND_TYPE}.json \
		--root ${NYDUS_LIB} \
		--address ${NYDUS_RUN}/containerd-nydus-grpc.sock \
		--log-level ${LEVEL} \
		--daemon-mode ${NYDUSD_DAEMON_MODE} \
		--log-to-stdout
fi

exec $@
