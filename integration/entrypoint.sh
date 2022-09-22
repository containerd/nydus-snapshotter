#!/bin/bash

# Copyright (c) 2022. Nydus Developers. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0


FSCACHE_NYDUSD_CONFIG=/etc/nydus/nydusd-config.fscache.json

CONTAINERD_ROOT=/var/lib/containerd/
CONTAINERD_STATUS=/run/containerd/
REMOTE_SNAPSHOTTER_SOCKET=/run/containerd-nydus/containerd-nydus-grpc.sock
REMOTE_SNAPSHOTTER_ROOT=/var/lib/containerd-nydus
CONTAINERD_SOCKET=/run/containerd/containerd.sock
SNAPSHOTTER_SHARED_MNT=${REMOTE_SNAPSHOTTER_ROOT}/mnt
SNAPSHOTTER_CACHE_DIR=${REMOTE_SNAPSHOTTER_ROOT}/cache

JAVA_IMAGE=${JAVA_IMAGE:-ghcr.io/dragonflyoss/image-service/java:nydus-nightly-v6}
WORDPRESS_IMAGE=${WORDPRESS_IMAGE:-ghcr.io/dragonflyoss/image-service/wordpress:nydus-nightly-v6}
TOMCAT_IMAGE=${TOMCAT_IMAGE:-ghcr.io/dragonflyoss/image-service/tomcat:nydus-nightly-v5}
STARGZ_IMAGE=${TOMCAT_IMAGE:-ghcr.io/stargz-containers/wordpress:5.9.2-esgz}

# JAVA_IMAGE=${JAVA_IMAGE:-hub.byted.org/gechangwei/java:latest-nydus-v6}
# WORDPRESS_IMAGE=${WORDPRESS_IMAGE:-hub.byted.org/gechangwei/wordpress:latest-nydus-v6}
# TOMCAT_IMAGE=${TOMCAT_IMAGE:-hub.byted.org/gechangwei/tomcat:latest-nydus-v5}
# STARGZ_IMAGE=${TOMCAT_IMAGE:-hub.byted.org/gechangwei/java:latest-stargz}

PLUGIN=nydus

RETRYNUM=30
RETRYINTERVAL=1
TIMEOUTSEC=180

GORACE_REPORT="$(pwd)/go_race_report"
export GORACE="log_path=${GORACE_REPORT}"

function detect_go_race {
    if [ -n "$(ls -A ${GORACE_REPORT}.* 2>/dev/null)" ]; then
        echo "go race detected"
        reports=$(ls -A ${GORACE_REPORT}.* 2>/dev/null)
        for r in ${reports}; do
            cat "$r"
        done
        exit 1
    fi
}

function stop_all_containers {
    containers=$(nerdctl ps -q | tr '\n' ' ')
    if [[ ${containers} == "" ]]; then
        return 0
    else
        echo "Killing containers ${containers}"
        for C in ${containers}; do
            nerdctl kill "${C}"
            nerdctl rm "${C}"
        done
        return 1
    fi
}

function pause {
    echo "I am going to wait for ${1} seconds only ..."
    sleep "${1}"
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

function can_erofs_ondemand_read {
    grep 'CONFIG_EROFS_FS_ONDEMAND=[ym]' /usr/src/linux-headers-"$(uname -r)"/.config
    return $?
}

function validate_mnt_number {
    expected="${1}"
    found=$(mount -t fuse | wc -l)
    if [[ $found != "$expected" ]]; then
        echo "expecting $expected mountpoints, but found $found"
        return 1
    else
        return 0
    fi
}

function reboot_containerd {
    killall "containerd" || true
    killall "containerd-nydus-grpc" || true

    # Let snapshotter shutdown all its services.
    sleep 0.5

    # FIXME
    echo "umount globally shared mountpoint"
    umount_global_shared_mnt

    rm -rf "${CONTAINERD_STATUS}"*
    rm -rf "${CONTAINERD_ROOT}"*
    if [ -f "${REMOTE_SNAPSHOTTER_SOCKET}" ]; then
        rm "${REMOTE_SNAPSHOTTER_SOCKET}"
    fi

    local daemon_mode=${1}
    local fs_driver=${2:-fusedev}
    local recover_policy=${3:-none}

    if [ -d "${REMOTE_SNAPSHOTTER_ROOT:?}/snapshotter/snapshots/" ]; then
        umount -t fuse --all
    fi

    if [[ "${fs_driver}" == fusedev ]]; then
        nydusd_config=/etc/nydus/config.json
    else
        nydusd_config="$FSCACHE_NYDUSD_CONFIG"
    fi

    # rm -rf "${REMOTE_SNAPSHOTTER_ROOT:?}"/* || fuser -m "${REMOTE_SNAPSHOTTER_ROOT}/mnt" && false
    rm -rf "${REMOTE_SNAPSHOTTER_ROOT:?}"/*
    containerd-nydus-grpc --daemon-mode "${daemon_mode}" --fs-driver "${fs_driver}" --recover-policy "${recover_policy}" --log-to-stdout --config-path /etc/nydus/config.json &

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
    umount "${SNAPSHOTTER_SHARED_MNT}" || true
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

    detect_go_race
}

function start_multiple_containers_multiple_daemons {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd mutiple

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"

    nerdctl_prune_images

    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"

    detect_go_race
}

function start_multiple_containers_shared_daemon {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"

    detect_go_race
}

function start_single_container_on_stargz {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared

    killall "containerd-nydus-grpc" || true
    sleep 0.5

    containerd-nydus-grpc --enable-stargz --daemon-mode multiple --fs-driver fusedev \
        --recover-policy none --log-to-stdout --config-path /etc/nydus/config.json &

    nerdctl --snapshotter nydus run -d --net none "${STARGZ_IMAGE}"
    detect_go_race
}

function pull_remove_one_image {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd mutiple

    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image rm "${JAVA_IMAGE}"

    detect_go_race
}

function pull_remove_multiple_images {
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

    # TODO: Validate running nydusd number

    detect_go_race
}

function start_multiple_containers_shared_daemon_erofs {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared fscache

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${TOMCAT_IMAGE}"

    detect_go_race
}

function kill_snapshotter_and_nydusd_recover {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}"

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    sleep 1

    echo "killing nydusd"
    killall -9 nydusd || true
    killall -9 containerd-nydus-grpc || true

    rm "${REMOTE_SNAPSHOTTER_SOCKET:?}"
    containerd-nydus-grpc --daemon-mode "${daemon_mode}" --log-to-stdout --config-path /etc/nydus/config.json &
    retry ls "${REMOTE_SNAPSHOTTER_SOCKET}"

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    detect_go_race
}

function only_restart_snapshotter {
    local daemon_mode=$1
    echo "testing $FUNCNAME ${daemon_mode}"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}"

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    echo "killing nydusd"
    killall -9 containerd-nydus-grpc || true

    rm "${REMOTE_SNAPSHOTTER_SOCKET:?}"
    containerd-nydus-grpc --daemon-mode "${daemon_mode}" --log-to-stdout --config-path /etc/nydus/config.json &
    retry ls "${REMOTE_SNAPSHOTTER_SOCKET}"

    if [[ "${daemon_mode}" == "shared" ]]; then
        validate_mnt_number 1
    else
        validate_mnt_number 2
    fi

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    detect_go_race
}

function kill_nydusd_recover_nydusd {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images

    reboot_containerd "${daemon_mode}" fusedev restart

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    pause 1

    echo "killing nydusd"
    killall -9 nydusd || true

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    detect_go_race
}

reboot_containerd mutiples

start_single_container_multiple_daemons
start_multiple_containers_multiple_daemons
start_multiple_containers_shared_daemon

pull_remove_one_image

pull_remove_multiple_images shared
pull_remove_multiple_images multiple

start_single_container_on_stargz

only_restart_snapshotter shared
only_restart_snapshotter multiple

kill_snapshotter_and_nydusd_recover shared
kill_snapshotter_and_nydusd_recover multiple

kill_nydusd_recover_nydusd shared
kill_nydusd_recover_nydusd multiple

if [[ $(can_erofs_ondemand_read) == 0 ]]; then
    start_multiple_containers_shared_daemon_erofs
fi

# trap "{ pause 1000; }" ERR
