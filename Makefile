all: clean build
optimizer: clean-optimizer build-optimizer

PKG = github.com/containerd/nydus-snapshotter
PACKAGES ?= $(shell go list ./... | grep -v /tests)
SUDO = $(shell which sudo)
GO_EXECUTABLE_PATH ?= $(shell which go)
NYDUS_BUILDER ?= /usr/bin/nydus-image
NYDUS_NYDUSD ?= /usr/bin/nydusd
GOOS ?= linux
GOARCH ?= $(shell go env GOARCH)
KERNEL_VER = $(shell uname -r)

# Used to populate variables in version package.
BUILD_TIMESTAMP=$(shell date '+%Y-%m-%dT%H:%M:%S')
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
REVISION=$(shell git rev-parse HEAD)$(shell if ! git diff --no-ext-diff --quiet --exit-code; then echo .m; fi)

# Relpace test target images for e2e tests.
ifdef E2E_TEST_TARGET_IMAGES_FILE
ENV_TARGET_IMAGES_FILE = --env-file ${E2E_TEST_TARGET_IMAGES_FILE}
endif

ifdef E2E_DOWNLOADS_MIRROR
BUILD_ARG_E2E_DOWNLOADS_MIRROR = --build-arg DOWNLOADS_MIRROR=${E2E_DOWNLOADS_MIRROR}
endif

ifdef GOPROXY
PROXY := GOPROXY="${GOPROXY}"
endif

ifdef FS_CACHE
FS_DRIVER = fscache
else
FS_DRIVER = fusedev
endif

SNAPSHOTTER_CONFIG=/etc/nydus/config.toml
SOURCE_SNAPSHOTTER_CONFIG=misc/snapshotter/config.toml
NYDUSD_CONFIG=/etc/nydus/nydusd-config.${FS_DRIVER}.json
SOURCE_NYDUSD_CONFIG=misc/snapshotter/nydusd-config.${FS_DRIVER}.json

SNAPSHOTTER_SYSTEMD_UNIT_SERVICE=misc/snapshotter/nydus-snapshotter.${FS_DRIVER}.service

LDFLAGS = -s -w -X ${PKG}/version.Version=${VERSION} -X ${PKG}/version.Revision=$(REVISION) -X ${PKG}/version.BuildTimestamp=$(BUILD_TIMESTAMP)
DEBUG_LDFLAGS = -X ${PKG}/version.Version=${VERSION} -X ${PKG}/version.Revision=$(REVISION) -X ${PKG}/version.BuildTimestamp=$(BUILD_TIMESTAMP)

CARGO ?= $(shell which cargo)
OPTIMIZER_SERVER = tools/optimizer-server
OPTIMIZER_SERVER_TOML = ${OPTIMIZER_SERVER}/Cargo.toml
OPTIMIZER_SERVER_BIN = ${OPTIMIZER_SERVER}/target/release/optimizer-server
STATIC_OPTIMIZER_SERVER_BIN = ${OPTIMIZER_SERVER}/target/x86_64-unknown-linux-gnu/release/optimizer-server

.PHONY: build
build:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/nydus-overlayfs ./cmd/nydus-overlayfs

debug:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(DEBUG_LDFLAGS)" -gcflags "-N -l" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(DEBUG_LDFLAGS)" -gcflags "-N -l" -v -o bin/nydus-overlayfs ./cmd/nydus-overlayfs

.PHONY: build-optimizer
build-optimizer:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/optimizer-nri-plugin ./cmd/optimizer-nri-plugin
	make -C tools/optimizer-server release && cp ${OPTIMIZER_SERVER_BIN} ./bin

static-release:
	CGO_ENABLED=0 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "$(LDFLAGS) -extldflags -static" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc
	CGO_ENABLED=0 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "$(LDFLAGS) -extldflags -static" -v -o bin/nydus-overlayfs ./cmd/nydus-overlayfs
	CGO_ENABLED=0 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "$(LDFLAGS) -extldflags -static" -v -o bin/optimizer-nri-plugin ./cmd/optimizer-nri-plugin
	make -C tools/optimizer-server static-release && cp ${STATIC_OPTIMIZER_SERVER_BIN} ./bin

# Majorly for cross build for converter package since it is imported by other projects
converter:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/converter ./cmd/converter

.PHONY: clean
clean:
	rm -f bin/*
	rm -rf _out

.PHONY: clean-optimizer
clean-optimizer:
	rm -rf bin/optimizer-nri-plugin bin/optimizer-server
	make -C tools/optimizer-server clean

.PHONY: install
install:
	@echo "+ $@ bin/containerd-nydus-grpc"
	@sudo install -D -m 755 bin/containerd-nydus-grpc /usr/local/bin/containerd-nydus-grpc
	@echo "+ $@ bin/nydus-overlayfs"
	@sudo install -D -m 755 bin/nydus-overlayfs /usr/local/bin/nydus-overlayfs

	@if [ ! -e ${NYDUSD_CONFIG} ]; then echo "+ $@ SOURCE_NYDUSD_CONFIG"; sudo install -D -m 664 ${SOURCE_NYDUSD_CONFIG} ${NYDUSD_CONFIG}; fi
	@if [ ! -e ${SNAPSHOTTER_CONFIG} ]; then echo "+ $@ ${SOURCE_SNAPSHOTTER_CONFIG}"; sudo install -D -m 664 ${SOURCE_SNAPSHOTTER_CONFIG} ${SNAPSHOTTER_CONFIG}; fi
	@sudo ln -f -s /etc/nydus/nydusd-config.${FS_DRIVER}.json /etc/nydus/nydusd-config.json

	@echo "+ $@ ${SNAPSHOTTER_SYSTEMD_UNIT_SERVICE}"
	@sudo install -D -m 644 ${SNAPSHOTTER_SYSTEMD_UNIT_SERVICE} /etc/systemd/system/nydus-snapshotter.service

	@sudo mkdir -p /etc/nydus/certs.d
	@if which systemctl >/dev/null; then sudo systemctl enable /etc/systemd/system/nydus-snapshotter.service; sudo systemctl restart nydus-snapshotter; fi

install-optimizer:
	sudo install -D -m 755 bin/optimizer-nri-plugin /opt/nri/plugins/02-optimizer-nri-plugin
	sudo install -D -m 755 bin/optimizer-server /usr/local/bin/optimizer-server
	sudo install -D -m 755 misc/example/optimizer-nri-plugin.conf /etc/nri/conf.d/02-optimizer-nri-plugin.conf

	@sudo mkdir -p /opt/nri/optimizer/results

.PHONY: vet
vet:
	go vet $(PACKAGES) ./tests

.PHONY: check
check: vet
	golangci-lint run

.PHONY: test
test:
	go test -race -v -mod=mod -cover ${PACKAGES}

.PHONY: cover
cover:
	go test -v -covermode=atomic -coverprofile=coverage.txt $(PACKAGES)
	go tool cover -func=coverage.txt

# make smoke TESTS=TestPack
smoke:
	${GO_EXECUTABLE_PATH} test -o smoke.tests -c -race -v -cover ./tests
	$(SUDO) -E NYDUS_BUILDER=${NYDUS_BUILDER} NYDUS_NYDUSD=${NYDUS_NYDUSD} ./smoke.tests -test.v -test.timeout 10m -test.parallel=8 -test.run=$(TESTS)

.PHONY: integration
integration:
	CGO_ENABLED=1 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags '-X "${PKG}/version.Version=${VERSION}" -extldflags "-static"' -race -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc
	CGO_ENABLED=1 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags '-X "${PKG}/version.Version=${VERSION}" -extldflags "-static"' -race -v -o bin/nydus-overlayfs ./cmd/nydus-overlayfs
	$(SUDO) DOCKER_BUILDKIT=1 docker build ${BUILD_ARG_E2E_DOWNLOADS_MIRROR} -t nydus-snapshotter-e2e:0.1 -f integration/Dockerfile .
	$(SUDO) docker run --cap-add SYS_ADMIN --security-opt seccomp=unconfined --cgroup-parent=system.slice --cgroupns private --name nydus-snapshotter_e2e --rm --privileged -v /root/.docker:/root/.docker -v `go env GOMODCACHE`:/go/pkg/mod \
	-v `go env GOCACHE`:/root/.cache/go-build -v `pwd`:/nydus-snapshotter \
	-v /usr/src/linux-headers-${KERNEL_VER}:/usr/src/linux-headers-${KERNEL_VER} \
	${ENV_TARGET_IMAGES_FILE}  \
	nydus-snapshotter-e2e:0.1
