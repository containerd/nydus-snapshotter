#!/bin/bash

set -euo pipefail

mkdir -p /opt/nydus/bin
install -m 755 /nydus-static/nydusd /nydus-static/nydusctl /nydus-static/nydus-image /opt/nydus/bin/

mkdir -p /etc/nydus
install -m 644 /nydus-static/configs/nydusd-config.json /etc/nydus/nydusd-config.json

host_setup_enabled="${NYDUS_HOST_SETUP_ENABLED:-false}"
containerd_socket="${NYDUS_CONTAINERD_SOCKET:-/run/containerd/containerd.sock}"
restart_marker="${NYDUS_RESTART_MARKER:-/run/containerd-nydus/.containerd-restart-required}"
nydus_socket="/run/containerd-nydus/containerd-nydus-grpc.sock"

wait_for_nydus_socket() {
    for _ in $(seq 1 60); do
        if [[ -S "${nydus_socket}" ]]; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_for_containerd() {
    for _ in $(seq 1 60); do
        if ctr --address "${containerd_socket}" version >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_for_nydus_plugin() {
    for _ in $(seq 1 60); do
        if ctr --address "${containerd_socket}" snapshots --snapshotter nydus ls >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

printf "Executing nydus-snapshotter...\n\n"

if [[ "${host_setup_enabled}" != "true" ]]; then
    exec ./containerd-nydus-grpc "$@"
fi

./containerd-nydus-grpc "$@" &
pid=$!

cleanup() {
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
}

if ! wait_for_nydus_socket; then
    printf "ERROR: nydus socket did not become ready in time\n" >&2
    cleanup
    exit 1
fi

if [[ -f "${restart_marker}" ]] || ! ctr --address "${containerd_socket}" snapshots --snapshotter nydus ls >/dev/null 2>&1; then
    printf "Reloading host containerd to activate the nydus snapshotter...\n"
    chroot /proc/1/root bash -c "systemctl restart containerd"

    if ! wait_for_containerd; then
        printf "ERROR: containerd did not become ready in time\n" >&2
        cleanup
        exit 1
    fi
fi

if ! wait_for_nydus_plugin; then
    printf "ERROR: containerd did not load the nydus snapshotter\n" >&2
    cleanup
    exit 1
fi

rm -f "${restart_marker}"
wait "${pid}"
