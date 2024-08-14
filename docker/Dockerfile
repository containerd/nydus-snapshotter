FROM hub.byted.org/infcplibrary/golang:1.22.1-bullseye-security as builder
ARG SOURCE_DIR=/nydus-snapshotter
ARG BUILD_FLAGS

WORKDIR ${SOURCE_DIR}

COPY . ${SOURCE_DIR}
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod/ \
make ${BUILD_FLAGS} && \
go install github.com/mikefarah/yq/v4@v4.44.3 && \
CGO_ENABLED=0 go build -ldflags "-s -w -extldflags -static" -v -o ./bin/rootfs-persister ./cmd/rootfs-persister

FROM hub.byted.org/infcplibrary/debian:bullseye-security

RUN echo '\
deb http://mirrors.volces.com/debian/ bullseye main non-free contrib\n\
deb-src http://mirrors.volces.com/debian/ bullseye main non-free contrib\n\
deb http://mirrors.volces.com/debian-security/ bullseye-security main\n\
deb-src http://mirrors.volces.com/debian-security/ bullseye-security main\n\
deb http://mirrors.volces.com/debian/ bullseye-updates main non-free contrib\n\
deb-src http://mirrors.volces.com/debian/ bullseye-updates main non-free contrib\n\
deb http://mirrors.volces.com/debian/ bullseye-backports main non-free contrib\n\
deb-src http://mirrors.volces.com/debian/ bullseye-backports main non-free contrib' > /etc/apt/sources.list

RUN apt update && apt -y install jq \
&& apt-get clean autoclean && apt-get autoremove --yes && rm -rf /var/lib/{apt,dpkg,cache,log}/

RUN echo '\
deb http://mirrors.ivolces.com/debian/ bullseye main non-free contrib\n\
deb-src http://mirrors.ivolces.com/debian/ bullseye main non-free contrib\n\
deb http://mirrors.ivolces.com/debian-security/ bullseye-security main\n\
deb-src http://mirrors.ivolces.com/debian-security/ bullseye-security main\n\
deb http://mirrors.ivolces.com/debian/ bullseye-updates main non-free contrib\n\
deb-src http://mirrors.ivolces.com/debian/ bullseye-updates main non-free contrib\n\
deb http://mirrors.ivolces.com/debian/ bullseye-backports main non-free contrib\n\
deb-src http://mirrors.ivolces.com/debian/ bullseye-backports main non-free contrib' > /etc/apt/sources.list

COPY --from=builder /nydus-snapshotter/bin/containerd-nydus-grpc /nydus-snapshotter/containerd-nydus-grpc
COPY --from=builder /go/bin/yq /usr/bin/yq
COPY --from=builder /nydus-snapshotter/bin/rootfs-persister /nydus-snapshotter/rootfs-persister

RUN export PACKAGE=infcp.containerimage.image_service_1.0.0.39.tar.gz && wget https://luban-source.byted.org/repository/scm/$PACKAGE && \
tar -xf $PACKAGE -C /nydus-snapshotter/ && rm $PACKAGE

# TODO: directly query containerd introspection service via go client
RUN export PACKAGE=infcp.vke.containerd_1.0.0.26.tar.gz && export CONTAINERD_VER=1.6.28-vke.13 && \
wget https://luban-source.byted.org/repository/scm/$PACKAGE && \
mkdir -p /containerd && tar -xf $PACKAGE -C /containerd && tar -xf /containerd/containerd-v$CONTAINERD_VER.tar.gz -C /containerd && \
tar -xf /containerd/containerd/files/containerd-$CONTAINERD_VER-linux-amd64.tar.gz -C /containerd && \
mv /containerd/bin/ctr /nydus-snapshotter && rm -rf /containerd && rm $PACKAGE

COPY misc/snapshotter/setup.sh /setup.sh
# TODO: Organize binaries and configs for different componenets
COPY misc/nri-rootfs-persister/rootfs-persister.conf /nydus-snapshotter/rootfs-persister.conf
RUN chmod +x /setup.sh

WORKDIR /nydus-snapshotter
ENTRYPOINT ["/setup.sh"]