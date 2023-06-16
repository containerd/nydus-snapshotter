#!/bin/bash

set -e

export RUSTUP_DIST_SERVER="https://rsproxy.cn"
export RUSTUP_UPDATE_ROOT="https://rsproxy.cn/rustup"

# install rust environment
curl --proto '=https' --tlsv1.2 -sSf https://rsproxy.cn/rustup-init.sh | sh -s -- -y
source "$HOME/.cargo/env"

make static-release

mkdir -p output

install -D -m 755 bin/containerd-nydus-grpc output
