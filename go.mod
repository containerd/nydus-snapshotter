module github.com/containerd/nydus-snapshotter

go 1.16

require (
	github.com/containerd/containerd v1.5.9
	github.com/containerd/continuity v0.2.2-0.20211201162329-8e53e7cac79d
	github.com/docker/cli v20.10.0-beta1.0.20201029214301-1d20b15adc38+incompatible
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
	github.com/google/go-containerregistry v0.5.1
	github.com/google/uuid v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.26.0
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/urfave/cli/v2 v2.3.0
	go.etcd.io/bbolt v1.3.6
	google.golang.org/grpc v1.41.0
)

replace (
	github.com/containerd/containerd => github.com/containerd/containerd v1.5.9
	github.com/opencontainers/runc => github.com/opencontainers/runc v1.0.3
)
