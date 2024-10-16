#!/usr/bin/env bash

#   Copyright The containerd Authors.

#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at

#       http://www.apache.org/licenses/LICENSE-2.0

#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.

set -o errexit -o errtrace -o functrace -o nounset -o pipefail

## Helpers specific to nydus

_rootful=

configure::rootful(){
  log::debug "Configuring rootful to: ${1:+true}"
  _rootful="${1:+true}"
}

configure::dockerd(){
  local config="$1"

  log::debug "Reconfiguring docker and restarting"
  log::debug "$config"
  sudo touch /etc/docker/daemon.json
  printf "%s" "$config" | sudo tee /etc/docker/daemon.json >/dev/null
  sudo systemctl restart docker
}

exec::docker(){
  local args=()
  [ ! "$_rootful" ] || args=(sudo)
  args+=(docker)

  log::debug "${args[*]} $*"
  "${args[@]}" "$@"
}

exec::kind(){
  local args=()
  [ ! "$_rootful" ] || args=(sudo)
  args+=(kind)

  log::debug "${args[*]} $*"
  "${args[@]}" "$@"
}

exec::kubectl(){
  local args=()
  [ ! "$_rootful" ] || args=(sudo)
  args+=(kubectl)

  log::debug "${args[*]} $*"
  "${args[@]}" "$@"
}

exec::nydusify(){
  local args=()
  [ ! "$_rootful" ] || args=(sudo)
  args+=(nydusify)

  log::debug "${args[*]} $*"
  "${args[@]}" "$@"
}

docker::configpath(){
  [ "$_rootful" ] && printf "/root/.docker/config.json" || printf "%s/.docker/config.json" "$HOME"
}

docker::login(){
  local user="$1"
  local password="$2"
  local registry_url="$3"

  local args=()
  [ ! "$_rootful" ] || args=(sudo)
  args+=(docker login --password-stdin)

  log::debug "${args[*]} --username=$user $registry_url"
  "${args[@]}" --username="$user" "$registry_url" <<<"$password" >/dev/null
}

# Installation helpers
install::kind(){
  local version="$1"
  local temp
  temp="$(fs::mktemp "install")"

  http::get "$temp"/kind "https://kind.sigs.k8s.io/dl/$version/kind-linux-${GOARCH:-amd64}"
  host::install "$temp"/kind
}

install::kubectl(){
  local version="${1:-v1.30.0}"
  [ "$version" ] || version="$(http::get /dev/stdout https://dl.k8s.io/release/stable.txt)"
  local temp
  temp="$(fs::mktemp "install")"

  http::get "$temp"/kubectl "https://dl.k8s.io/release/$version/bin/linux/${GOARCH:-amd64}/kubectl"
  host::install "$temp"/kubectl
}

install::nydus(){
  local version="$1"
  local temp
  temp="$(fs::mktemp "install")"

  http::get "$temp"/nydus-static.tgz "https://github.com/dragonflyoss/nydus/releases/download/$version/nydus-static-$version-linux-${GOARCH:-amd64}.tgz"
  tar::expand "$temp" "$temp"/nydus-static.tgz
  host::install "$temp"/nydus-static/nydus-image
  host::install "$temp"/nydus-static/nydusify
  host::install "$temp"/nydus-static/nydusd
  host::install "$temp"/nydus-static/nydusctl
}

install::nerdctl(){
  local version="$1"
  local temp
  temp="$(fs::mktemp "install")"

  http::get "$temp"/nerdctl.tar.gz "https://github.com/containerd/nerdctl/releases/download/v$version/nerdctl-$version-linux-${GOARCH:-amd64}.tar.gz"
  tar::expand "$temp" "$temp"/nerdctl.tar.gz
  host::install "$temp"/nerdctl
}

start::registry(){
  local user="$1"
  local password="$2"
  local tempdir
  tempdir="$(fs::mktemp registry)"
  local port=5000
  local ip

  exec::docker rm -f registry-auth-"$port" >/dev/null 2>&1 || true

  exec::docker run \
    --rm \
    --entrypoint htpasswd \
    httpd:2 -Bbn "$user" "$password" > "$tempdir"/htpasswd

  exec::docker run -d \
    -p "$port":5000 \
    --restart=always \
    --name registry-auth-"$port" \
    -v "$tempdir":/auth \
    -e "REGISTRY_AUTH=htpasswd" \
    -e "REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm" \
    -e REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd \
    registry:2 >/dev/null

  ip="$(ip addr show eth0 | grep 'inet ' | awk '{print $2}' | cut -d/ -f1)"
  printf "%s:%s" "$ip" "$port"
}
