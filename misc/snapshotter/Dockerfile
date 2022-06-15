FROM ubuntu:20.04 AS sourcer

RUN apt update; apt install --no-install-recommends -y curl wget ca-certificates
RUN export NYDUS_VERSION=$(curl --silent "https://api.github.com/repos/dragonflyoss/image-service/releases/latest" | grep -Po '"tag_name": "\K.*?(?=")'); \
    wget https://github.com/dragonflyoss/image-service/releases/download/$NYDUS_VERSION/nydus-static-$NYDUS_VERSION-linux-amd64.tgz; \
    tar xzf nydus-static-$NYDUS_VERSION-linux-amd64.tgz
RUN mv nydus-static/* /; mv nydusd-fusedev nydusd

FROM ubuntu:20.04

WORKDIR /root/
RUN mkdir -p /usr/local/bin/ /etc/nydus/ /var/lib/nydus/cache/
COPY --from=sourcer /nydusd /nydus-image /usr/local/bin/
COPY containerd-nydus-grpc /usr/local/bin/
COPY nydusd-config.json /etc/nydus/config.json
COPY nydusd-config-localfs.json /etc/nydus/localfs.json
RUN apt update && \
    apt install --no-install-recommends -y ca-certificates && \
    rm -rf /var/cache/apt/* /var/lib/apt/lists/*
COPY entrypoint.sh /

ENTRYPOINT ["/entrypoint.sh"]
