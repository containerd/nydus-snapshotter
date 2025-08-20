#!/usr/bin/env bash
set -o errexit -o errtrace -o functrace -o nounset -o pipefail

AUTH_TYPE="${AUTH_TYPE:-kubeconf}"
[ "$(uname -m)" == "aarch64" ] && GOARCH=arm64 || GOARCH=amd64
ROOTFUL="${ROOTFUL:-}"

root="$(cd "$(dirname "${BASH_SOURCE[0]:-$PWD}")" 2>/dev/null 1>&2 && pwd)"
readonly root
# shellcheck source=/dev/null
. "$root/lib.sh"
# shellcheck source=/dev/null
. "$root/helpers.sh"


KIND_VERSION=v0.23.0
NYDUS_VERSION=v2.3.0
DOCKER_USER=testuser
DOCKER_PASSWORD=testpassword
NAMESPACE=nydus-system

log::info "Configuring rootful and github"
configure::rootful "${ROOTFUL:-}"
github::settoken '${{ secrets.GITHUB_TOKEN }}'

NYDUS_VERSION="$(github::releases::latest dragonflyoss/nydus | jq -rc 'select(.tag_name != null) | .tag_name' || printf "%s" "$NYDUS_VERSION")"
log::info "Get latest nydus version $NYDUS_VERSION"

# Build
log::info "Building... make"
# Binaries are going to be copied over inside the kind container that likely has a different glibc we do not control.
# So, build static
if [ "$LOG_LEVEL" == debug ]; then
  make -d static
else
  make -s static
fi
cp bin/containerd-nydus-grpc ./
cp bin/nydus-overlayfs ./
cp -r misc/snapshotter/* ./

log::info "Building... docker image"
pwd
exec::docker build --build-arg NYDUS_VER="$NYDUS_VERSION" -t local-dev:e2e .

# Start local registry, and configure docker
log::info "Starting registry"
registry_url="$(start::registry "$DOCKER_USER" "$DOCKER_PASSWORD")"
# Configure template
sed -e "s|REGISTRY_URL|$registry_url|" tests/e2e/k8s/test-pod.yaml.tpl > tests/e2e/k8s/test-pod.yaml
configure::dockerd '{
  "exec-opts": ["native.cgroupdriver=cgroupfs"],
  "cgroup-parent": "/actions_job",
  "insecure-registries" : [ "'"$registry_url"'" ]
}'
http::healthcheck "$registry_url"/v2/ 10 2 "$DOCKER_USER" "$DOCKER_PASSWORD"
log::info "Login"
docker::login "$DOCKER_USER" "$DOCKER_PASSWORD" "$registry_url"

# Install dependencies
log::info "Installing host dependencies"
install::kind "$KIND_VERSION"
install::kubectl
install::nydus "$NYDUS_VERSION"

# Convert a nydus image and push it
log::info "Converting test image to nydus and push"
exec::nydusify convert \
   --source busybox:latest \
   --target $registry_url/busybox:nydus-v6-latest \
   --fs-version 6

# Create fresh cluster
log::info "Creating new cluster"
exec::kind delete cluster 2>/dev/null || true
exec::kind create cluster
exec::kind load docker-image local-dev:e2e

# Deploy nydus
log::info "Deploying nydus"
exec::kubectl create -f tests/e2e/k8s/snapshotter-"$AUTH_TYPE".yaml
pod="$(exec::kubectl --namespace "$NAMESPACE" get pods --no-headers -o custom-columns=NAME:metadata.name)"
exec::kubectl --namespace "$NAMESPACE" wait po "$pod" --for=condition=ready --timeout=1m

# Reconfigure and restart kind containerd
log::info "Restarting containerd"
echo '[plugins."io.containerd.grpc.v1.cri".registry.mirrors."'"$registry_url"'"]
          endpoint = ["http://'"$registry_url"'"]' |
  exec::docker exec -i kind-control-plane sh -c 'cat /dev/stdin >> /etc/containerd/config.toml'

exec::docker exec kind-control-plane systemctl restart containerd

# Actual testing
exec::kubectl delete --namespace "$NAMESPACE" secret generic regcred 2>/dev/null || true
exec::kubectl create --namespace "$NAMESPACE" secret generic regcred \
  --from-file=.dockerconfigjson="$(docker::configpath)" \
  --type=kubernetes.io/dockerconfigjson

if [ "$AUTH_TYPE" == "cri" ]; then
  exec::docker exec kind-control-plane sh -c 'echo " --image-service-endpoint=unix:///run/containerd-nydus/containerd-nydus-grpc.sock" >> /etc/default/kubelet'
  exec::docker exec kind-control-plane sh -c 'systemctl daemon-reload && systemctl restart kubelet'
fi

exec::kubectl apply -f tests/e2e/k8s/test-pod.yaml
exec::kubectl wait po test-pod --namespace nydus-system --for=condition=ready --timeout=1m || {
  exec::kubectl --namespace nydus-system get pods
  exec::kubectl --namespace nydus-system get events
  exec::kubectl --namespace nydus-system logs nydus-snapshotter
  exec::kubectl --namespace nydus-system logs test-pod
  exit 1
}
exec::kubectl delete -f tests/e2e/k8s/test-pod.yaml

