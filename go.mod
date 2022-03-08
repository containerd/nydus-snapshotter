module github.com/containerd/nydus-snapshotter

go 1.16

require (
	github.com/containerd/containerd v1.6.1
	github.com/containerd/continuity v0.2.2
	github.com/docker/cli v20.10.0-beta1.0.20201029214301-1d20b15adc38+incompatible
	github.com/docker/docker v1.6.1 // indirect
	github.com/docker/libcontainer v2.2.1+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
	github.com/google/go-containerregistry v0.5.1
	github.com/google/uuid v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.30.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/urfave/cli/v2 v2.3.0
	go.etcd.io/bbolt v1.3.6
	golang.org/x/sys v0.0.0-20211216021012-1d35b9e2eb4e
	google.golang.org/grpc v1.43.0
)

replace (
	cloud.google.com/go => cloud.google.com/go v0.81.0
	github.com/containerd/containerd => github.com/containerd/containerd v1.6.1
	github.com/opencontainers/runc => github.com/opencontainers/runc v1.0.3
)
