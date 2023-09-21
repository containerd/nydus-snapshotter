#!/bin/bash

# Copyright (c) 2022. Nydus Developers. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0

set -eEuo pipefail

FSCACHE_NYDUSD_CONFIG=/etc/nydus/nydusd-config.fscache.json
FUSE_NYDUSD_LOCALFS_CONFIG=/etc/nydus/nydusd-config-localfs.json
SNAPSHOTTER_CONFIG=/etc/nydus/config.toml

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
STARGZ_IMAGE=${STARGZ_IMAGE:-ghcr.io/stargz-containers/wordpress:5.9.2-esgz}
REDIS_OCI_IMAGE=${REDIS_OCI_IMAGE:-ghcr.io/stargz-containers/redis:6.2.6-org}
WORDPRESS_OCI_IMAGE=${WORDPRESS_OCI_IMAGE:-ghcr.io/dragonflyoss/image-service/wordpress:latest}

PLUGIN=nydus

RETRYNUM=30
RETRYINTERVAL=1
TIMEOUTSEC=180

GORACE_REPORT="$(pwd)/go_race_report"
export GORACE="log_path=${GORACE_REPORT}"

# trap "{ pause 1000; }" ERR

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
            nerdctl kill "${C}" || true
            nerdctl stop "${C}" || true
            nerdctl rm "${C}" || true
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
    return 1
    # grep 'CONFIG_EROFS_FS_ONDEMAND=[ym]' /usr/src/linux-headers-"$(uname -r)"/.config 1>/dev/null
    # echo $?
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

function set_config_option {
    KEY="${1}"
    VALUE="${2}"

    sed -i "s/\($KEY *= *\).*/\1$VALUE/" "${SNAPSHOTTER_CONFIG}"
}

function set_recover_policy {
    policy="${1}"

    set_config_option "recover_policy" \"${policy}\"
}

function set_enable_referrer_detect {
    set_config_option "enable_referrer_detect" "true"
}

function reboot_containerd {
    killall "containerd" || true
    killall "containerd-nydus-grpc" || true
    # In case nydusd is using cache dir
    killall "nydusd" || true

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
        nydusd_config=/etc/nydus/nydusd-config.json
    else
        nydusd_config="$FSCACHE_NYDUSD_CONFIG"
    fi

    # Override nydus configuration, this configuration is usually set by each case
    if [[ -n ${NYDUS_CONFIG_PATH:-} ]]; then
        nydusd_config=${NYDUS_CONFIG_PATH}
    fi

    # rm -rf "${REMOTE_SNAPSHOTTER_ROOT:?}"/* || fuser -m "${REMOTE_SNAPSHOTTER_ROOT}/mnt" && false
    rm -rf "${REMOTE_SNAPSHOTTER_ROOT:?}"/*

    set_recover_policy "${recover_policy}"

    containerd-nydus-grpc --log-to-stdout \
        --daemon-mode "${daemon_mode}" --fs-driver "${fs_driver}" \
        --config "${SNAPSHOTTER_CONFIG}" --nydusd-config "${nydusd_config}" &

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
    # With fscache driver, 2.1 nydusd don't have API to release the cache files.
    # Thy locate at directory ${SNAPSHOTTER_CACHE_DIR}/cache
    if [[ $(ls -A -p "${SNAPSHOTTER_CACHE_DIR}" | grep -v /) == "" ]]; then
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
    reboot_containerd multiple

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"

    detect_go_race
}

function start_multiple_containers_multiple_daemons {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd multiple

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
    reboot_containerd multiple

    killall "containerd-nydus-grpc" || true
    sleep 0.5

    containerd-nydus-grpc --enable-stargz --daemon-mode multiple --fs-driver fusedev \
        --recover-policy none --log-to-stdout --config-path /etc/nydus/nydusd-config.json &

    nerdctl --snapshotter nydus run -d --net none "${STARGZ_IMAGE}"
    detect_go_race
}

function start_container_on_oci {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd multiple

    nerdctl --snapshotter nydus run -d --net none "${REDIS_OCI_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_OCI_IMAGE}"
    pause 2

    func_retry stop_all_containers

    # Deleteing with flag --async as a fuzzer
    nerdctl image rm --async --force "${REDIS_OCI_IMAGE}"
    nerdctl image rm --force "${WORDPRESS_OCI_IMAGE}"
}

function start_container_with_referrer_detect {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd multiple

    set_enable_referrer_detect
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_OCI_IMAGE}"

    detect_go_race
}

function pull_remove_one_image {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd multiple

    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image rm "${JAVA_IMAGE}"

    detect_go_race
}

function pull_remove_multiple_images {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}"

    # Because nydusd is not started right after image pull.
    # Nydusd is started when preparing the writable active snapshot as the
    # uppermost layer. So we must create a container to start nydusd.
    # Then to test if snapshotter's nydusd daemons management works well

    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${TOMCAT_IMAGE}"

    nerdctl --snapshotter nydus create --rm --net none "${TOMCAT_IMAGE}"
    nerdctl --snapshotter nydus create --rm --net none "${WORDPRESS_IMAGE}"

    nerdctl --snapshotter nydus image rm --force "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus image rm --force "${WORDPRESS_IMAGE}"

    # Deleteing with flag --async as a fuzzer
    nerdctl --snapshotter nydus image rm --force --async "${TOMCAT_IMAGE}"
    nerdctl --snapshotter nydus image pull "${TOMCAT_IMAGE}"
    nerdctl --snapshotter nydus create --net none "${TOMCAT_IMAGE}"

    detect_go_race

    # TODO: Validate running nydusd number
}

function start_multiple_containers_shared_daemon_fscache {
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd shared fscache

    nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}"
    nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}"

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

    echo "killing nydus-snapshotter"
    killall -9 containerd-nydus-grpc || true

    rm "${REMOTE_SNAPSHOTTER_SOCKET:?}"
    containerd-nydus-grpc --config "${SNAPSHOTTER_CONFIG}" \
        --daemon-mode "${daemon_mode}" --log-to-stdout --config-path /etc/nydus/nydusd-config.json &
    retry ls "${REMOTE_SNAPSHOTTER_SOCKET}"

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    detect_go_race
}

# No restart or failover recover policy. Just let snapshotter start a new nydusd when it refreshes.
function fscache_kill_snapshotter_and_nydusd_recover {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}" fscache

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    sleep 1

    echo "killing nydusd"
    killall -9 nydusd || true
    killall -9 containerd-nydus-grpc || true

    sleep 1

    rm "${REMOTE_SNAPSHOTTER_SOCKET:?}"
    containerd-nydus-grpc --log-to-stdout --config "${SNAPSHOTTER_CONFIG}" \
        --daemon-mode "${daemon_mode}" --fs-driver fscache --config-path /etc/nydus/nydusd-config.fscache.json &
    retry ls "${REMOTE_SNAPSHOTTER_SOCKET}"

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    # killall -9 nydusd
    sleep 0.2
    detect_go_race
}

function fscache_kill_nydusd_failover() {
    local daemon_mode=shared
    echo "testing $FUNCNAME"
    nerdctl_prune_images
    reboot_containerd "${daemon_mode}" fscache failover

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    killall -9 nydusd

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    sleep 1

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

    echo "killing snapshotter"
    killall -9 containerd-nydus-grpc || true

    rm "${REMOTE_SNAPSHOTTER_SOCKET:?}"
    containerd-nydus-grpc --config "${SNAPSHOTTER_CONFIG}" --daemon-mode \
        "${daemon_mode}" --log-to-stdout --config-path /etc/nydus/nydusd-config.json &
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

function ctr_snapshot_usage {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images

    reboot_containerd "${daemon_mode}" fusedev restart

    nerdctl --snapshotter nydus image pull "${WORDPRESS_IMAGE}"
    nerdctl --snapshotter nydus image pull "${JAVA_IMAGE}"
    c1=$(nerdctl --snapshotter nydus create --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus create --net none "${WORDPRESS_IMAGE}")

    pause 1

    ctr snapshot --snapshotter nydus ls
    ctr snapshot --snapshotter nydus usage

    echo "start new containers"
    nerdctl --snapshotter nydus start "$c1"
    nerdctl --snapshotter nydus start "$c2"

    ctr snapshot --snapshotter nydus ls
    ctr snapshot --snapshotter nydus usage

    detect_go_race
}

function kill_multiple_nydusd_recover_failover {
    local daemon_mode=$1
    echo "testing $FUNCNAME"
    nerdctl_prune_images

    reboot_containerd "${daemon_mode}" fusedev failover

    c1=$(nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}")

    pause 1

    nerdctl kill "$c1" || true
    nerdctl kill "$c2 " || true

    echo "killing nydusd"
    killall -9 nydusd || true

    echo "start new containers"

    c1=$(nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}")

    pause 1

    nerdctl kill "$c1" || true
    nerdctl kill "$c2 " || true

    echo "killing nydusd again"
    killall -9 nydusd || true

    c1=$(nerdctl --snapshotter nydus run -d --net none "${JAVA_IMAGE}")
    c2=$(nerdctl --snapshotter nydus run -d --net none "${WORDPRESS_IMAGE}")

    detect_go_race
}

# Refer to https://github.com/moby/moby/blob/088afc99e4bf8adb78e29733396182417d67ada2/hack/dind#L28-L38
function enable_nesting_for_cgroup_v2() {
    if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
        mkdir -p /sys/fs/cgroup/init
        xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs || :
        sed -e 's/ / +/g' -e 's/^/-/' </sys/fs/cgroup/cgroup.controllers \
            >/sys/fs/cgroup/cgroup.subtree_control
    fi
}

enable_nesting_for_cgroup_v2

reboot_containerd multiple

start_single_container_multiple_daemons
start_multiple_containers_multiple_daemons
start_multiple_containers_shared_daemon

pull_remove_one_image

pull_remove_multiple_images shared
pull_remove_multiple_images multiple

# start_single_container_on_stargz

only_restart_snapshotter shared
only_restart_snapshotter multiple

kill_snapshotter_and_nydusd_recover shared
kill_snapshotter_and_nydusd_recover multiple

ctr_snapshot_usage multiple
ctr_snapshot_usage shared

if [[ $(can_erofs_ondemand_read) == 0 ]]; then
    kill_multiple_nydusd_recover_failover multiple
    kill_multiple_nydusd_recover_failover shared

    start_multiple_containers_shared_daemon_fscache
    fscache_kill_snapshotter_and_nydusd_recover shared
    fscache_kill_nydusd_failover
fi

start_container_on_oci

start_container_with_referrer_detect
