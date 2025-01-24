#!/usr/bin/env bash
# Copyright (c) 2023. Nydus Developers. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

set -o errexit
set -o pipefail
set -o nounset

SNAPSHOTTER_ARTIFACTS_DIR="/opt/nydus-artifacts"

# Container runtime config, the default container runtime is containerd
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-containerd}"
CONTAINER_RUNTIME_CONFIG="/etc/containerd/config.toml"

# Common nydus snapshotter config options
FS_DRIVER="${FS_DRIVER:-fusedev}"
SNAPSHOTTER_GRPC_SOCKET="${SNAPSHOTTER_GRPC_SOCKET:-/run/containerd-nydus/containerd-nydus-grpc.sock}"

# The directory about nydus and nydus snapshotter
NYDUS_CONFIG_DIR="${NYDUS_CONFIG_DIR:-/etc/nydus}"
NYDUS_LIB_DIR="${NYDUS_LIB_DIR:-/var/lib/containerd/io.containerd.snapshotter.v1.nydus}"
NYDUS_BINARY_DIR="${NYDUS_BINARY_DIR:-/usr/local/bin}"
SNAPSHOTTER_SCRYPT_DIR="${SNAPSHOTTER_SCRYPT_DIR:-/opt/nydus}"

# The binary about nydus-snapshotter
SNAPSHOTTER_BINARY="${SNAPSHOTTER_BINARY:-${NYDUS_BINARY_DIR}/containerd-nydus-grpc}"

# The config about nydus snapshotter
SNAPSHOTTER_CONFIG="${SNAPSHOTTER_CONFIG:-${NYDUS_CONFIG_DIR}/config.toml}"
# The systemd service config about nydus snapshotter
SNAPSHOTTER_SERVICE="${SNAPSHOTTER_SERVICE:-/etc/systemd/system/nydus-snapshotter.service}"
# If true, the script would read the config from env.
ENABLE_CONFIG_FROM_VOLUME="${ENABLE_CONFIG_FROM_VOLUME:-false}"
# If true, the script would enable the "runtime specific snapshotter" in containerd config.
ENABLE_RUNTIME_SPECIFIC_SNAPSHOTTER="${ENABLE_RUNTIME_SPECIFIC_SNAPSHOTTER:-false}"
# If true, the snapshotter would be running as a systemd service
ENABLE_SYSTEMD_SERVICE="${ENABLE_SYSTEMD_SERVICE:-false}"

COMMANDLINE=""

# If we fail for any reason a message will be displayed
die() {
    msg="$*"
    echo "ERROR: $msg" >&2
    exit 1
}

print_usage() {
    echo "Usage: $0 [deploy/cleanup]"
}

wait_service_active(){
    local wait_time="$1"
    local sleep_time="$2"
    local service="$3"

    nsenter -t 1 -m systemctl restart $service

    # Wait for containerd to be running
    while [ "$wait_time" -gt 0 ]; do
        if nsenter -t 1 -m systemctl is-active --quiet $service; then
            echo "$service is running"
            return 0  
        else
            sleep "$sleep_time"
            wait_time=$((wait_time-sleep_time))
        fi
    done

    echo "Timeout reached. $service may not be running."
    nsenter -t 1 -m systemctl status $service
    return 1  
}

function fs_driver_handler() {
    if [ "${ENABLE_CONFIG_FROM_VOLUME}" == "true" ]; then
        SNAPSHOTTER_CONFIG="${NYDUS_CONFIG_DIR}/config.toml"
    else
        case "${FS_DRIVER}" in
        fusedev) 
            sed -i -e "s|nydusd_config = .*|nydusd_config = \"${NYDUS_CONFIG_DIR}/nydusd-fusedev.json\"|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|fs_driver = .*|fs_driver = \"fusedev\"|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|daemon_mode = .*|daemon_mode = \"multiple\"|" "${SNAPSHOTTER_CONFIG}" 
            ;;
        fscache) 
            sed -i -e "s|nydusd_config = .*|nydusd_config = \"${NYDUS_CONFIG_DIR}/nydusd-fscache.json\"|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|fs_driver = .*|fs_driver = \"fscache\"|" "${SNAPSHOTTER_CONFIG}"
            sed -i -e "s|daemon_mode = .*|daemon_mode = \"multiple\"|" "${SNAPSHOTTER_CONFIG}"  
            ;;
        blockdev) 
            sed -i -e "s|fs_driver = .*|fs_driver = \"blockdev\"|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|enable_kata_volume = .*|enable_kata_volume = true|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|enable_tarfs = .*|enable_tarfs = true|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|daemon_mode = .*|daemon_mode = \"none\"|" "${SNAPSHOTTER_CONFIG}"  
            sed -i -e "s|export_mode = .*|export_mode = \"layer_block_with_verity\"|" "${SNAPSHOTTER_CONFIG}"  
            ;;
        proxy) 
            sed -i -e "s|fs_driver = .*|fs_driver = \"proxy\"|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|enable_kata_volume = .*|enable_kata_volume = true|" "${SNAPSHOTTER_CONFIG}" 
            sed -i -e "s|daemon_mode = .*|daemon_mode = \"none\"|" "${SNAPSHOTTER_CONFIG}"  
            ;;
        *) die "invalid fs driver ${FS_DRIVER}" ;;
        esac
    fi
    COMMANDLINE+=" --config ${SNAPSHOTTER_CONFIG}"
}

function configure_snapshotter() {

    echo "configuring snapshotter"

    # Copy the container runtime config to a backup
    cp "$CONTAINER_RUNTIME_CONFIG" "$CONTAINER_RUNTIME_CONFIG".bak.nydus


    # When trying to edit the config file that is mounted by docker with `sed -i`, the error would happend:
    # sed: cannot rename /etc/containerd/config.tomlpmdkIP: Device or resource busy  
    # The reason is that `sed`` with option `-i` creates new file, and then replaces the old file with the new one, 
    # which definitely will change the file inode. But the file is mounted by docker, which means we are not allowed to 
    # change its inode from within docker container.
    # 
    # So we copy the original file to a backup, make changes to the backup, and then overwrite the original file with the backup.
    cp "$CONTAINER_RUNTIME_CONFIG" "$CONTAINER_RUNTIME_CONFIG".bak
    # Check and add nydus proxy plugin in the config
    if grep -q '\[proxy_plugins.nydus\]' "$CONTAINER_RUNTIME_CONFIG".bak; then
        echo "the config has configured the nydus proxy plugin!"
    else
        echo "Not found nydus proxy plugin!"
        cat <<EOF >>"$CONTAINER_RUNTIME_CONFIG".bak
        
    [proxy_plugins.nydus]
        type = "snapshot"
        address = "$SNAPSHOTTER_GRPC_SOCKET"
EOF
    fi

    if grep -q 'disable_snapshot_annotations' "$CONTAINER_RUNTIME_CONFIG".bak; then
        sed -i -e "s|disable_snapshot_annotations = .*|disable_snapshot_annotations = false|" \
                "${CONTAINER_RUNTIME_CONFIG}".bak
    else
        sed -i '/\[plugins\..*\.containerd\]/a\disable_snapshot_annotations = false' \
                "${CONTAINER_RUNTIME_CONFIG}".bak
    fi
    if grep -q 'discard_unpacked_layers' "$CONTAINER_RUNTIME_CONFIG".bak; then
        sed -i -e "s|discard_unpacked_layers = .*|discard_unpacked_layers = false|" \
                "${CONTAINER_RUNTIME_CONFIG}".bak
    else
        sed -i '/\[plugins\..*\.containerd\]/a\discard_unpacked_layers = false' \
                "${CONTAINER_RUNTIME_CONFIG}".bak
    fi

    if [ "${ENABLE_RUNTIME_SPECIFIC_SNAPSHOTTER}" == "false" ]; then
        sed -i -e '/\[plugins\..*\.containerd\]/,/snapshotter =/ s/snapshotter = "[^"]*"/snapshotter = "nydus"/' "${CONTAINER_RUNTIME_CONFIG}".bak
    fi
    
    cat "${CONTAINER_RUNTIME_CONFIG}".bak >  "${CONTAINER_RUNTIME_CONFIG}"
}

function install_snapshotter() {
    echo "install nydus snapshotter artifacts"
    find "${SNAPSHOTTER_ARTIFACTS_DIR}${NYDUS_BINARY_DIR}" -type f -exec install -Dm 755 -t "${NYDUS_BINARY_DIR}" "{}"  \;
    find "${SNAPSHOTTER_ARTIFACTS_DIR}${NYDUS_CONFIG_DIR}" -type f -exec install -Dm 644 -t "${NYDUS_CONFIG_DIR}" "{}"  \;
    install -D -m 644 "${SNAPSHOTTER_ARTIFACTS_DIR}${SNAPSHOTTER_SCRYPT_DIR}/snapshotter.sh" "${SNAPSHOTTER_SCRYPT_DIR}/snapshotter.sh"
    if [ "${ENABLE_SYSTEMD_SERVICE}" == "true" ]; then
        install -D -m 644 "${SNAPSHOTTER_ARTIFACTS_DIR}${SNAPSHOTTER_SERVICE}" "${SNAPSHOTTER_SERVICE}"
    fi
    if [ "${ENABLE_CONFIG_FROM_VOLUME}" == "true" ]; then
        find "/etc/nydus-snapshotter" -type f -exec install -Dm 644 -t "${NYDUS_CONFIG_DIR}" "{}"  \;
    fi
}

function deploy_snapshotter() {
    echo "deploying snapshotter"
    install_snapshotter

    COMMANDLINE="${SNAPSHOTTER_BINARY}"
    fs_driver_handler
    configure_snapshotter
    if [ "${ENABLE_SYSTEMD_SERVICE}" == "true" ]; then
        echo "running snapshotter as systemd service"
        sed -i "s|^ExecStart=.*$|ExecStart=$COMMANDLINE|" "${SNAPSHOTTER_SERVICE}"
        nsenter -t 1 -m systemctl daemon-reload
        nsenter -t 1 -m systemctl enable nydus-snapshotter.service
        wait_service_active 30 5 nydus-snapshotter
    else
        echo "running snapshotter as standalone process"
        ${COMMANDLINE} &
    fi
    wait_service_active 30 5 ${CONTAINER_RUNTIME}

}

function remove_images() {
    local SNAPSHOTTER="nydus"
    local NAMESPACE="k8s.io"
    local ctr_args="nsenter -t 1 -m ctr"

    if [[ " k3s k3s-agent rke2-agent rke2-server " =~ " ${CONTAINER_RUNTIME} " ]]; then
        ctr_args+=" --address /run/k3s/containerd/containerd.sock"
    fi
    ctr_args+=" --namespace $NAMESPACE"

    # List all snapshots for nydus snapshotter
    local SNAPSHOTS=$($ctr_args snapshot --snapshotter $SNAPSHOTTER ls | awk 'NR>1 {print $1}')
    echo "Images associated with snapshotter $SNAPSHOTTER:"

    # Loop through each snapshot and find associated contents
    for SNAPSHOT in $SNAPSHOTS; do
        local CONTENTS=$($ctr_args content ls | grep $SNAPSHOT | awk '{print $1}')
        echo "Snapshot: $SNAPSHOT, Contents: $CONTENTS"
        if [ -z "$CONTENTS" ]; then
            continue
        fi
        # Loop through each content and find associated digests of images
        for CONTENT in $CONTENTS; do
            local DIGESTS=$($ctr_args image ls | grep $CONTENT | awk '{print $3}')
            echo "Content: $CONTENT, Digests: $DIGESTS"
            if [ -z "$DIGESTS" ]; then
                continue
            fi
            # Loop through each digest and find associated image references
            for DIGEST in $DIGESTS; do
                local IMAGES=$($ctr_args image ls | grep $DIGEST | awk '{print $1}')
                echo "Digest: $DIGEST, Images: $IMAGES"
                if [ -z "$IMAGES" ]; then
                    continue
                fi
                for IMAGE in $IMAGES; do
                    # Delete the image
                    $ctr_args images rm $IMAGE > /dev/null 2>&1 || true
                    echo "Image $IMAGE removed"
                done
            done
            # Delete the content
            $ctr_args content rm $CONTENT > /dev/null 2>&1 || true
            echo "content $CONTENT removed"
        done
        # Delete the snapshot
        $ctr_args snapshot --snapshotter $SNAPSHOTTER rm $SNAPSHOT > /dev/null 2>&1 || true
        echo "snapshot $SNAPSHOT removed"
    done
    echo "INFO: Images removed"
}

function cleanup_snapshotter() {
    echo "cleaning up snapshotter"

    pid=$(ps -ef | grep containerd-nydus-grpc | grep -v grep | awk '{print $1}' || true)
    if [ ! -z "$pid" ]; then
        remove_images
    fi
    echo "Recover containerd config"
    cat "$CONTAINER_RUNTIME_CONFIG".bak.nydus >"$CONTAINER_RUNTIME_CONFIG"
    if [ "${ENABLE_SYSTEMD_SERVICE}" == "true" ]; then
        nsenter -t 1 -m systemctl stop nydus-snapshotter.service
        nsenter -t 1 -m systemctl disable --now nydus-snapshotter.service
        rm -f "${SNAPSHOTTER_SERVICE}" || true
    else
        kill -9 $pid || true
    fi
    wait_service_active 30 5 ${CONTAINER_RUNTIME}
    echo "Removing nydus-snapshotter artifacts from host"
    rm -f "${NYDUS_BINARY_DIR}"/nydus*
    rm -rf "${NYDUS_CONFIG_DIR}"/*
    rm -rf "${SNAPSHOTTER_SCRYPT_DIR}"/*
    rm -rf "${NYDUS_LIB_DIR}"/*
    echo "cleaned up snapshotter"
}

function get_container_runtime() {
    local runtime=$(kubectl get node ${NODE_NAME} -o jsonpath='{.status.nodeInfo.containerRuntimeVersion}')
    if [ "$?" -ne 0 ]; then
        die "\"$NODE_NAME\" is an invalid node name"
    fi

    if echo "$runtime" | grep -qE 'containerd.*-k3s'; then
        if nsenter -t 1 -m systemctl is-active --quiet rke2-agent; then
            echo "rke2-agent"
        elif nsenter -t 1 -m systemctl is-active --quiet rke2-server; then
            echo "rke2-server"
        elif nsenter -t 1 -m systemctl is-active --quiet k3s-agent; then
            echo "k3s-agent"
        else
            echo "k3s"
        fi
    elif nsenter -t 1 -m systemctl is-active --quiet k0scontroller; then
        echo "k0s-controller"
    elif nsenter -t 1 -m systemctl is-active --quiet k0sworker; then
        echo "k0s-worker"
    else
        echo "$runtime" | awk -F '[:]' '{print $1}'
    fi
}

function main() {
    # script requires that user is root
    euid=$(id -u)
    if [[ $euid -ne 0 ]]; then
        die "This script must be run as root"
    fi

    CONTAINER_RUNTIME=$(get_container_runtime)
    if [[ " k3s k3s-agent rke2-agent rke2-server " =~ " ${CONTAINER_RUNTIME} " ]]; then
        CONTAINER_RUNTIME_CONFIG_TMPL="${CONTAINER_RUNTIME_CONFIG}.tmpl"
        if [ ! -f "${CONTAINER_RUNTIME_CONFIG_TMPL}" ]; then
            cp "${CONTAINER_RUNTIME_CONFIG}" "${CONTAINER_RUNTIME_CONFIG_TMPL}"
        fi

        CONTAINER_RUNTIME_CONFIG="${CONTAINER_RUNTIME_CONFIG_TMPL}"
    elif [ "${CONTAINER_RUNTIME}" == "containerd" ]; then
        if [ ! -f "${CONTAINER_RUNTIME_CONFIG}" ]; then
            mkdir -p $(dirname ${CONTAINER_RUNTIME_CONFIG}) || true
            if [ -x $(command -v ${CONTAINER_RUNTIME}) ]; then
                ${CONTAINER_RUNTIME} config default > ${CONTAINER_RUNTIME_CONFIG}
            else
                die "Not able to find an executable ${CONTAINER_RUNTIME} binary to create the default config"
            fi
        fi
    else
        die "${CONTAINER_RUNTIME} is a unsupported containe runtime"
    fi

    action=${1:-}
    if [ -z "$action" ]; then
        print_usage
        die "invalid arguments"
    fi

    case "$action" in
    deploy)
        deploy_snapshotter
        ;;
    cleanup)
        cleanup_snapshotter
        ;;
    *)
        die "invalid arguments"
        print_usage
        ;;
    esac

    sleep infinity
}

main "$@"
