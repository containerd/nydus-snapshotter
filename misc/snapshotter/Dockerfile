FROM alpine:3.17.0 AS base

FROM base AS sourcer
ARG TARGETARCH
ARG NYDUS_VER=v2.3.0

RUN apk add -q --no-cache curl && \
    apk add -q --no-cache --upgrade grep && \
    curl -fsSL -O https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VER/nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    echo $NYDUS_VER > /.nydus_version && \
    tar xzf nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    rm nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    mv nydus-static/* / \
    && rm -rf /nydus-overlayfs

FROM base AS kubectl-sourcer
ARG TARGETARCH

RUN apk add -q --no-cache curl && \
    curl -fsSL -o /usr/bin/kubectl https://dl.k8s.io/release/"$(curl -L -s https://dl.k8s.io/release/stable.txt)"/bin/linux/"$TARGETARCH"/kubectl && \
    chmod +x /usr/bin/kubectl

FROM base
ARG DESTINATION=/opt/nydus-artifacts
ARG CONFIG_DESTINATION=${DESTINATION}/etc/nydus
ARG BINARY_DESTINATION=${DESTINATION}/usr/local/bin
ARG SCRIPT_DESTINATION=${DESTINATION}/opt/nydus

WORKDIR /root/
RUN apk add -q --no-cache libc6-compat bash

VOLUME /var/lib/containerd/io.containerd.snapshotter.v1.nydus
VOLUME /run/containerd-nydus

COPY --from=sourcer /.nydus_version /.nydus_version
COPY --from=kubectl-sourcer /usr/bin/kubectl /usr/bin/kubectl

RUN mkdir -p ${CONFIG_DESTINATION} ${BINARY_DESTINATION} ${SCRIPT_DESTINATION} /var/lib/containerd/io.containerd.snapshotter.v1.nydus/cache /tmp/blobs/
COPY --from=sourcer /nydus* ${BINARY_DESTINATION}/
COPY --chmod=755 containerd-nydus-grpc nydus-overlayfs ${BINARY_DESTINATION}/
COPY --chmod=755 snapshotter.sh ${SCRIPT_DESTINATION}/snapshotter.sh
COPY nydusd-config.fusedev.json ${CONFIG_DESTINATION}/nydusd-fusedev.json
COPY nydusd-config-localfs.json ${CONFIG_DESTINATION}/nydusd-localfs.json
COPY nydusd-config.fscache.json ${CONFIG_DESTINATION}/nydusd-fscache.json
COPY config.toml ${CONFIG_DESTINATION}/config.toml
COPY nydus-snapshotter.service ${DESTINATION}/etc/systemd/system/nydus-snapshotter.service
