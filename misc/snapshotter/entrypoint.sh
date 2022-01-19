#! /bin/bash

set -eu

if [ "$#" -eq 0 ]; then
	containerd-nydus-grpc \
	    --log-level trace \
	    --nydusd-path /usr/local/bin/nydusd \
	    --config-path /etc/nydus/config.json \
	    --root /var/lib/containerd-test/io.containerd.snapshotter.v1.nydus \
	    --address /var/lib/containerd-test/io.containerd.snapshotter.v1.nydus/containerd-nydus-grpc.sock \
	    --enable-nydus-overlayfs \
	    --log-to-stdout
fi

exec "$@"
