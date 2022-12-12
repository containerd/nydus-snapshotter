FROM alpine:3.17.0 AS sourcer

RUN apk add --no-cache curl
RUN apk add --no-cache --upgrade grep
RUN export NYDUS_VERSION=$(curl --silent "https://api.github.com/repos/dragonflyoss/image-service/releases/latest" | grep -Po '"tag_name": "\K.*?(?=")') && \
    wget https://github.com/dragonflyoss/image-service/releases/download/$NYDUS_VERSION/nydus-static-$NYDUS_VERSION-linux-amd64.tgz && \
    echo $NYDUS_VERSION > /.nydus_version && \
    tar xzf nydus-static-$NYDUS_VERSION-linux-amd64.tgz && \
    rm nydus-static-$NYDUS_VERSION-linux-amd64.tgz
RUN mv nydus-static/* /

FROM alpine:3.17.0

WORKDIR /root/

RUN apk add --no-cache libc6-compat

VOLUME /var/lib/containerd-nydus /run/containerd-nydus

COPY --from=sourcer /.nydus_version /.nydus_version

RUN mkdir -p /usr/local/bin/ /etc/nydus/ /var/lib/containerd-nydus/cache /tmp/blobs/
COPY --from=sourcer /nydus* /usr/local/bin/
COPY containerd-nydus-grpc /usr/local/bin/
RUN chmod +x /usr/local/bin/containerd-nydus-grpc
COPY nydusd-config.fusedev.json /etc/nydus/config.json
COPY nydusd-config-localfs.json /etc/nydus/localfs.json
COPY entrypoint.sh /

ENTRYPOINT ["/entrypoint.sh"]
