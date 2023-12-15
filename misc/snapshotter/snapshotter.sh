#!/usr/bin/env bash
# Copyright (c) 2023. Nydus Developers. All rights reserved.
#
# SPDX-License-Identifier: Apache-2.0
#

set -o errexit
set -o pipefail
set -o nounset

# Common nydus snapshotter config options
FS_DRIVER="${FS_DRIVER:-fusedev}"

# The directory about nydus and nydus snapshotter
NYDUS_CONFIG_DIR="${NYDUS_CONFIG_DIR:-/etc/nydus}"
NYDUS_LIB_DIR="${NYDUS_LIB_DIR:-/var/lib/containerd-nydus}"
NYDUS_BINARY_DIR="${NYDUS_BINARY_DIR:-/usr/local/bin}"
SNAPSHOTTER_SCRYPT_DIR="${SNAPSHOTTER_SCRYPT_DIR:-/opt/nydus}"

# The binary about nydus-snapshotter
SNAPSHOTTER_BINARY="${SNAPSHOTTER_BINARY:-${NYDUS_BINARY_DIR}/containerd-nydus-grpc}"

COMMANDLINE=""

# If we fail for any reason a message will be displayed
die() {
    msg="$*"
    echo "ERROR: $msg" >&2
    exit 1
}

function fs_driver_handler() {

    case "${FS_DRIVER}" in
    fusedev) SNAPSHOTTER_CONFIG="${NYDUS_CONFIG_DIR}/config-fusedev.toml" ;;
    fscache) SNAPSHOTTER_CONFIG="${NYDUS_CONFIG_DIR}/config-fscache.toml" ;;
    blockdev) SNAPSHOTTER_CONFIG="${NYDUS_CONFIG_DIR}/config-blockdev.toml" ;;
    proxy) SNAPSHOTTER_CONFIG="${NYDUS_CONFIG_DIR}/config-proxy.toml" ;;
    *) die "invalid fs driver ${FS_DRIVER}" ;;
    esac
    COMMANDLINE+=" --config ${SNAPSHOTTER_CONFIG}"
}

function deploy_snapshotter() {
    echo "deploying snapshotter"
    COMMANDLINE="${SNAPSHOTTER_BINARY}"
    fs_driver_handler
    ${COMMANDLINE}
}
