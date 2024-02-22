FROM alpine:3.17.0 AS base

FROM base AS sourcer
ARG NYDUS_VER=v2.2.4

RUN apk add --no-cache curl && \
    apk add --no-cache --upgrade grep && \
    curl -OL https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VER/nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    echo $NYDUS_VER > /.nydus_version && \
    tar xzf nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    rm nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    mv nydus-static/* / \
    && rm -rf /nydus-overlayfs

FROM base AS kubectl-sourcer
RUN apk add --no-cache curl && \
    ARCH=$(uname -m) && \
    if [ "${ARCH}" = "x86_64" ]; then ARCH=amd64; fi && \
    if [ "${ARCH}" = "aarch64" ]; then ARCH=arm64; fi && \
    curl -fL --progress-bar -o /usr/bin/kubectl https://storage.googleapis.com/kubernetes-release/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${ARCH}/kubectl && \
    chmod +x /usr/bin/kubectl

FROM base
ARG DESTINATION=/opt/nydus-artifacts
ARG CONFIG_DESTINATION=${DESTINATION}/etc/nydus
ARG BINARY_DESTINATION=${DESTINATION}/usr/local/bin
ARG SCRIPT_DESTINATION=${DESTINATION}/opt/nydus

WORKDIR /root/
RUN apk add --no-cache libc6-compat bash

VOLUME /var/lib/containerd-nydus /run/containerd-nydus

COPY --from=sourcer /.nydus_version /.nydus_version
COPY --from=kubectl-sourcer /usr/bin/kubectl /usr/bin/kubectl

RUN mkdir -p ${CONFIG_DESTINATION} ${BINARY_DESTINATION} ${SCRIPT_DESTINATION} /var/lib/containerd-nydus/cache /tmp/blobs/
COPY --from=sourcer /nydus* ${BINARY_DESTINATION}/
COPY containerd-nydus-grpc nydus-overlayfs ${BINARY_DESTINATION}/
COPY snapshotter.sh ${SCRIPT_DESTINATION}/snapshotter.sh
RUN chmod +x ${BINARY_DESTINATION}/containerd-nydus-grpc ${BINARY_DESTINATION}/nydus-overlayfs ${SCRIPT_DESTINATION}/snapshotter.sh
COPY nydusd-config.fusedev.json ${CONFIG_DESTINATION}/nydusd-fusedev.json
COPY nydusd-config-localfs.json ${CONFIG_DESTINATION}/nydusd-localfs.json
COPY nydusd-config.fscache.json ${CONFIG_DESTINATION}/nydusd-fscache.json
COPY config.toml ${CONFIG_DESTINATION}/config.toml
COPY nydus-snapshotter.service ${DESTINATION}/etc/systemd/system/nydus-snapshotter.service
