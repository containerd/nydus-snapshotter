#!/bin/bash

set -e

make static-release

mkdir -p output

install -D -m 755 bin/containerd-nydus-grpc output
