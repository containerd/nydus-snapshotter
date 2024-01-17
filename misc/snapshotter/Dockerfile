FROM alpine:3.17.0 AS sourcer

ARG NYDUS_VER=v2.1.5

RUN apk add --no-cache curl && \
    apk add --no-cache --upgrade grep && \
    curl -OL https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VER/nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    echo $NYDUS_VER > /.nydus_version && \
    tar xzf nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    rm nydus-static-$NYDUS_VER-linux-amd64.tgz && \
    mv nydus-static/* /

FROM alpine:3.17.0
ARG DESTINATION=/opt/nydus-artifacts
ARG CONFIG_DESTINATION=${DESTINATION}/etc/nydus
ARG BINARY_DESTINATION=${DESTINATION}/usr/local/bin
ARG SCRIPT_DESTINATION=${DESTINATION}/opt/nydus

WORKDIR /root/
RUN apk add --no-cache libc6-compat bash

VOLUME /var/lib/containerd-nydus /run/containerd-nydus

COPY --from=sourcer /.nydus_version /.nydus_version

RUN mkdir -p ${CONFIG_DESTINATION} ${BINARY_DESTINATION} ${SCRIPT_DESTINATION} /var/lib/containerd-nydus/cache /tmp/blobs/
COPY --from=sourcer /nydus* ${BINARY_DESTINATION}/
COPY containerd-nydus-grpc ${BINARY_DESTINATION}/
COPY snapshotter.sh ${SCRIPT_DESTINATION}/snapshotter.sh
RUN chmod +x ${BINARY_DESTINATION}/containerd-nydus-grpc ${SCRIPT_DESTINATION}/snapshotter.sh
COPY nydusd-config.fusedev.json ${CONFIG_DESTINATION}/nydusd-fusedev.json
COPY nydusd-config-localfs.json ${CONFIG_DESTINATION}/nydusd-localfs.json
COPY nydusd-config.fscache.json ${CONFIG_DESTINATION}/nydusd-fscache.json
COPY config.toml ${CONFIG_DESTINATION}/config.toml
COPY nydus-snapshotter.service ${DESTINATION}/etc/systemd/system/nydus-snapshotter.service

