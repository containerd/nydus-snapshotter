FROM golang:1.24.3-bookworm AS builder
ARG SOURCE_DIR=/nydus-snapshotter
ARG BUILD_FLAGS
ARG GOPROXY

WORKDIR ${SOURCE_DIR}

COPY . ${SOURCE_DIR}
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod/ make GOPROXY=${GOPROXY} && \
GOPROXY=${GOPROXY} go install github.com/mikefarah/yq/v4@v4.44.3

FROM debian:bookworm
ARG NYDUS_VER=v2.3.0

RUN apt update && apt -y install jq curl wget procps && \ 
apt-get clean autoclean && apt-get autoremove --yes && rm -rf /var/lib/{apt,dpkg,cache,log}/

COPY --from=builder /nydus-snapshotter/bin/containerd-nydus-grpc /nydus-snapshotter/containerd-nydus-grpc
COPY --from=builder /go/bin/yq /usr/bin/yq

RUN  wget https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VER/nydus-static-$NYDUS_VER-linux-amd64.tgz && \
echo $NYDUS_VER > /.nydus_version && \
mkdir -p /nydus-static && \
tar xzf nydus-static-$NYDUS_VER-linux-amd64.tgz  && \
rm nydus-static-$NYDUS_VER-linux-amd64.tgz

RUN wget https://github.com/containerd/containerd/releases/download/v2.1.1/containerd-2.1.1-linux-amd64.tar.gz && \
mkdir -p /tmp/containerd && \
tar xzf containerd-2.1.1-linux-amd64.tar.gz -C /tmp/containerd && \
install -D -m 755 /tmp/containerd/bin/ctr /usr/local/bin/ && \
rm -rf containerd-2.1.1-linux-amd64.tar.gz /tmp/containerd

RUN wget https://github.com/TomWright/dasel/releases/download/v2.8.1/dasel_linux_amd64 && install -D -m 755 dasel_linux_amd64 /usr/local/bin/dasel

COPY misc/snapshotter/setup.sh /setup.sh
RUN chmod +x /setup.sh

WORKDIR /nydus-snapshotter
ENTRYPOINT ["/setup.sh"]