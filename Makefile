all: clear build

PACKAGES ?= $(shell go list ./... | grep -v /tests)
SUDO = $(shell which sudo)
GO_EXECUTABLE_PATH ?= $(shell which go)
NYDUS_BUILDER ?= /usr/bin/nydus-image
NYDUS_NYDUSD ?= /usr/bin/nydusd
GOOS ?= linux
GOARCH ?= $(shell go env GOARCH)
KERNEL_VER = $(shell uname -r)
#GOPROXY ?= https://goproxy.io

# Used to populate variables in version package.
BUILD_TIMESTAMP=$(shell date '+%Y-%m-%dT%H:%M:%S')
VERSION=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always --tags)
REVISION=$(shell git rev-parse HEAD)$(shell if ! git diff --no-ext-diff --quiet --exit-code; then echo .m; fi)

ifdef GOPROXY
PROXY := GOPROXY="${GOPROXY}"
endif

ifdef FS_CACHE
FS_DRIVER = fscache
else
FS_DRIVER = fusedev
endif

LDFLAGS = -s -w -X main.Version=${VERSION} -X main.Reversion=$(REVISION) -X main.BuildTimestamp=$(BUILD_TIMESTAMP)

.PHONY: build
build:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc

static-release:
	CGO_ENABLED=0 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags "$(LDFLAGS) -extldflags -static" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc

# Majorly for cross build for converter package since it is imported by other projects
converter:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags "$(LDFLAGS)" -v -o bin/converter ./cmd/converter

.PHONY: clear
clear:
	rm -f bin/*
	rm -rf _out

.PHONY: install
install:
	sudo install -D -m 755 bin/containerd-nydus-grpc /usr/local/bin/containerd-nydus-grpc
	sudo install -D -m 755 misc/snapshotter/nydusd-config.${FS_DRIVER}.json /etc/nydus/config.json
	sudo install -D -m 644 misc/snapshotter/nydus-snapshotter.${FS_DRIVER}.service /etc/systemd/system/nydus-snapshotter.service

	@if which systemctl; then sudo systemctl enable /etc/systemd/system/nydus-snapshotter.service;fi

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

smoke:
	$(SUDO) NYDUS_BUILDER=${NYDUS_BUILDER} NYDUS_NYDUSD=${NYDUS_NYDUSD} ${GO_EXECUTABLE_PATH} test -race -v ./tests
	$(SUDO) NYDUS_BUILDER=${NYDUS_BUILDER} NYDUS_NYDUSD=${NYDUS_NYDUSD} ${GO_EXECUTABLE_PATH} test -race -v ./tests

.PHONY: integration
integration:
	CGO_ENABLED=1 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags '-X "main.Version=${VERSION}" -extldflags "-static"' -race -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc
	$(SUDO) DOCKER_BUILDKIT=1 docker build -t nydus-snapshotter-e2e:0.1 -f integration/Dockerfile .
	$(SUDO) docker run --name nydus-snapshotter_e2e --rm --privileged -v /root/.docker:/root/.docker -v `go env GOMODCACHE`:/go/pkg/mod \
	-v `go env GOCACHE`:/root/.cache/go-build -v `pwd`:/nydus-snapshotter \
	-v /usr/src/linux-headers-${KERNEL_VER}:/usr/src/linux-headers-${KERNEL_VER} nydus-snapshotter-e2e:0.1
