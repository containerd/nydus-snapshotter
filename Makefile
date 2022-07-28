all: clear build

VERSION=$(shell git rev-parse --verify HEAD --short=7)
BRANCH=$(shell git rev-parse --abbrev-ref HEAD)
PACKAGES ?= $(shell go list ./... | grep -v /tests)
SUDO = $(shell which sudo)
GO_EXECUTABLE_PATH ?= $(shell which go)
NYDUS_BUILDER ?= /usr/bin/nydus-image
NYDUS_NYDUSD ?= /usr/bin/nydusd-fusedev
GOOS ?= linux
GOARCH ?= amd64
#GOPROXY ?= https://goproxy.io

ifdef GOPROXY
PROXY := GOPROXY="${GOPROXY}"
endif

ifdef FS_CACHE
FS_DRIVER = fscache
else
FS_DRIVER = fusedev
endif


.PHONY: build
build:
	GOOS=${GOOS} GOARCH=${GOARCH} ${PROXY} go build -ldflags="-s -w -X 'main.Version=${VERSION}'" -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc

static-release:
	CGO_ENABLED=0 ${PROXY} GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags '-s -w -X "main.Version=${VERSION}" -extldflags "-static"' -v -o bin/containerd-nydus-grpc ./cmd/containerd-nydus-grpc

.PHONY: clear
clear:
	rm -f bin/*
	rm -rf _out

.PHONY: install
install: static-release
	sudo install -D -m 755 bin/containerd-nydus-grpc /usr/local/bin/containerd-nydus-grpc
	sudo install -D -m 755 misc/snapshotter/nydusd-config.${FS_DRIVER}.json /etc/nydus/config.json
	sudo install -D -m 644 misc/snapshotter/nydus-snapshotter.${FS_DRIVER}.service /etc/systemd/system/nydus-snapshotter.service
	sudo systemctl enable /etc/systemd/system/nydus-snapshotter.service

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
	$(SUDO) NYDUS_BUILDER=${NYDUS_BUILDER} NYDUS_NYDUSD=${NYDUS_NYDUSD} ${GO_EXECUTABLE_PATH} test -race -v ./tests -args -fs-version=6
