module github.com/containerd/nydus-snapshotter

go 1.24.3

toolchain go1.24.4

require (
	dario.cat/mergo v1.0.2
	github.com/AdaLogics/go-fuzz-headers v0.0.0-20240806141605-e8a1dd7889d6
	github.com/KarpelesLab/reflink v1.0.1
	github.com/aliyun/aliyun-oss-go-sdk v3.0.2+incompatible
	github.com/aws/aws-sdk-go-v2 v1.30.3
	github.com/aws/aws-sdk-go-v2/config v1.27.27
	github.com/aws/aws-sdk-go-v2/credentials v1.17.27
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.17.8
	github.com/aws/aws-sdk-go-v2/service/s3 v1.58.2
	github.com/containerd/cgroups/v3 v3.0.5
	github.com/containerd/containerd v1.7.27
	github.com/containerd/containerd/api v1.9.0
	github.com/containerd/containerd/v2 v2.1.2
	github.com/containerd/continuity v0.4.5
	github.com/containerd/errdefs v1.0.0
	github.com/containerd/fifo v1.1.0
	github.com/containerd/log v0.1.0
	github.com/containerd/nri v0.8.0
	github.com/containerd/platforms v1.0.0-rc.1
	github.com/containerd/plugin v1.0.0
	github.com/containerd/stargz-snapshotter v0.15.2-0.20240709063920-1dac5ef89319
	github.com/containerd/stargz-snapshotter/estargz v0.15.2-0.20240709063920-1dac5ef89319
	github.com/containers/ocicrypt v1.2.1
	github.com/distribution/reference v0.6.0
	github.com/docker/cli v27.1.0+incompatible
	github.com/freddierice/go-losetup v0.0.0-20220711213114-2a14873012db
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da
	github.com/google/go-containerregistry v0.20.1
	github.com/gorilla/mux v1.8.1
	github.com/hashicorp/go-retryablehttp v0.7.7
	github.com/klauspost/compress v1.18.0
	github.com/moby/locker v1.0.1
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/opencontainers/runtime-spec v1.2.1
	github.com/pelletier/go-toml v1.9.5
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.22.0
	github.com/prometheus/client_model v0.6.1
	github.com/rs/xid v1.5.0
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.10.0
	github.com/urfave/cli/v2 v2.27.6
	go.etcd.io/bbolt v1.4.0
	golang.org/x/exp v0.0.0-20241108190413-2d47ceb2692f
	golang.org/x/net v0.38.0
	golang.org/x/sync v0.15.0
	golang.org/x/sys v0.33.0
	google.golang.org/grpc v1.72.2
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gotest.tools v2.2.0+incompatible
	k8s.io/api v0.32.3
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.32.3
	k8s.io/cri-api v0.33.0
	k8s.io/utils v0.0.0-20241104100929-3ea5e8cea738
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/Microsoft/hcsshim v0.13.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.3 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.11 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.15 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.0 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.11.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.3.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.11.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.17.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.22.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.26.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.30.3 // indirect
	github.com/aws/smithy-go v1.20.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cilium/ebpf v0.16.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/ttrpc v1.2.7 // indirect
	github.com/containerd/typeurl/v2 v2.2.3 // indirect
	github.com/coreos/go-systemd/v22 v22.5.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.5 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.3+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.7.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/emicklei/go-restful/v3 v3.12.2 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-jose/go-jose/v4 v4.0.5 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/miekg/pkcs11 v1.1.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/moby/sys/mountinfo v0.7.2 // indirect
	github.com/moby/sys/sequential v0.6.0 // indirect
	github.com/moby/sys/signal v0.7.1 // indirect
	github.com/moby/sys/user v0.4.0 // indirect
	github.com/moby/sys/userns v0.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/selinux v1.12.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/smallstep/pkcs7 v0.1.1 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/stefanberger/go-pkcs11uri v0.0.0-20230803200340-78284954bff6 // indirect
	github.com/vbatts/tar-split v0.11.5 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.60.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/oauth2 v0.27.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.7.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250218202821-56aae31c358a // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20241105132330-32ad38e42d3f // indirect
	sigs.k8s.io/json v0.0.0-20241010143419-9aa6b5e7a4b3 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.2 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

retract (
	v0.11.3
	v0.11.2
	v0.11.1
	v0.11.0
	v0.3.0 // retagged: cd604c1b597558ea045a79c4f80a8c780909801b -> 85653575c7dafb6b06548478ee1dc61ac5905d00
)

exclude (
	// These dependencies were updated to "master" in some modules we depend on,
	// but have no code-changes since their last release. Unfortunately, this also
	// causes a ripple effect, forcing all users of the containerd module to also
	// update these dependencies to an unrelease / un-tagged version.
	//
	// Both these dependencies will unlikely do a new release in the near future,
	// so exclude these versions so that we can downgrade to the current release.
	//
	// For additional details, see this PR and links mentioned in that PR:
	// https://github.com/kubernetes-sigs/kustomize/pull/5830#issuecomment-2569960859
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2
)
