#!/bin/bash

# Copyright (c) 2022. Nydus Developers. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

CONTAINERD_ROOT=/var/lib/containerd/
CONTAINERD_STATUS=/run/containerd/
REMOTE_SNAPSHOTTER_SOCKET=/run/containerd-nydus/containerd-nydus-grpc.sock
REMOTE_SNAPSHOTTER_ROOT=/var/lib/containerd-nydus-grpc
CONTAINERD_SOCKET=/run/containerd/containerd.sock
SNAPSHOTTER_SHARED_MNT=${REMOTE_SNAPSHOTTER_ROOT}/mnt
SNAPSHOTTER_CACHE_DIR=${REMOTE_SNAPSHOTTER_ROOT}/cache

JAVA_IMAGE=${JAVA_IMAGE:-ghcr.io/dragonflyoss/image-service/java:nydus-nightly-v6}
WORDPRESS_IMAGE=${WORDPRESS_IMAGE:-ghcr.io/dragonflyoss/image-service/wordpress:nydus-nightly-v6}
TOMCAT_IMAGE=${TOMCAT_IMAGE:-ghcr.io/dragonflyoss/image-service/tomcat:nydus-nightly-v5}

# JAVA_IMAGE=${JAVA_IMAGE:-hub.byted.org/gechangwei/java:latest-nydus-v6}
# WORDPRESS_IMAGE=${WORDPRESS_IMAGE:-hub.byted.org/gechangwei/wordpress:latest-nydus-v6}
# TOMCAT_IMAGE=${TOMCAT_IMAGE:-hub.byted.org/gechangwei/tomcat:latest-nydus-v5}

PLUGIN=nydus

RETRYNUM=30
RETRYINTERVAL=1
TIMEOUTSEC=180

function stop_all_containers {
    containers=$(nerdctl ps -q | tr '\n' ' ')
    if [[ ${containers} == "" ]]; then
        return 0
    else
        echo "Killing containers ${containers}"
        for C in ${containers}; do
            nerdctl kill "${C}"
        done
        return 1
    fi
}

function func_retry {
    local SUCCESS=false
    for i in $(seq ${RETRYNUM}); do
        if "${*}"; then
            SUCCESS=true
            break
        fi
        echo "Fail(${i}). Retrying function..."
        sleep ${RETRYINTERVAL}
    done
    if [ "${SUCCESS}" == "true" ]; then
        return 0
    else
        return 1
    fi
}

function retry {
    local SUCCESS=false
    for i in $(seq ${RETRYNUM}); do
        if eval "timeout ${TIMEOUTSEC} ${@}"; then
            SUCCESS=true
            break
        fi
        echo "Fail(${i}). Retrying..."
        sleep ${RETRYINTERVAL}
    done
    if [ "${SUCCESS}" == "true" ]; then
        return 0
    else
        return 1
    fi
}

function reboot_containerd {
    killall "containerd" || true
    killall "containerd-nydus-grpc" || true

    # FIXME
    umount_global_shared_mnt

    rm -rf "${CONTAINERD_STATUS}"*
    rm -rf "${CONTAINERD_ROOT}"*
    if [ -f "${REMOTE_SNAPSHOTTER_SOCKET}" ]; then
        rm "${REMOTE_SNAPSHOTTER_SOCKET}"
    fi

    local daemon_mode=${1}

    if [ -d "${REMOTE_SNAPSHOTTER_ROOT:?}/snapshotter/snapshots/" ]; then
        umount -t fuse --all
    fi

    rm -rf "${REMOTE_SNAPSHOTTER_ROOT:?}"/*

    containerd-nydus-grpc --daemon-mode "${daemon_mode}" --log-to-stdout --config-path /etc/nydus/config.json &

    retry ls "${REMOTE_SNAPSHOTTER_SOCKET}"
    containerd --log-level info --config=/etc/containerd/config.toml &
    retry ls "${CONTAINERD_SOCKET}"

    # Makes sure containerd and containerd-nydus-grpc are up-and-running.
    UNIQUE_SUFFIX=$(date +%s%N | shasum | base64 | fold -w 10 | head -1)
    retry ctr snapshots --snapshotter="${PLUGIN}" prepare "connectiontest-dummy-${UNIQUE_SUFFIX}" ""
}

function restart_snapshotter {
    killall -INT containerd-nydus-grpc
    local daemon_mode=$1
}

function umount_global_shared_mnt {
    umount -f "${SNAPSHOTTER_SHARED_MNT}" || true
}

function is_cache_cleared {
    if [[ $(ls -A "${SNAPSHOTTER_CACHE_DIR}") == "" ]]; then
        true
    else
        echo "ERROR: Cache is not cleared"
        false
    fi
}

function nerdctl_prune_images {
    # Wait for containers observation.
    sleep 1
    func_retry stop_all_containers
    nerdctl container prune -f
    nerdctl image prune --all -f
    nerdctl images
    is_cache_cleared
}

function start_single_container_multiple_daemons {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd mutiple

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
}

function start_multiple_containers_multiple_daemons {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd mutiple

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"
}

function start_multiple_containers_shared_daemon {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"
}

function start_single_container_on_stargz {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared

    nerdctl --snapshotter nydus run -d --net none ghcr.io/stargz-containers/wordpress:5.9.2-esgz
}

function pull_reomve_one_image {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd mutiple

    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image rm "${JAVA_IMAGE}"
}

function pull_reomve_multiple_images {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}"

    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${TOMCAT_IMAGE}"

    nerdctl --snapshotter nydus image rm "${TOMCAT_IMAGE}"
    nerdctl --snapshotter nydus image rm "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image rm "${WORDPRESS_IMAGE}"
}

reboot_containerd mutiples

start_single_container_multiple_daemons
start_multiple_containers_multiple_daemons
start_multiple_containers_shared_daemon
pull_reomve_one_image
pull_reomve_multiple_images shared
pull_reomve_multiple_images mutiple
# start_single_container_on_stargz
