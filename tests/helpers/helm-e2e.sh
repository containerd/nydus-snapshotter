#!/usr/bin/env bash

set -o errexit -o errtrace -o functrace -o nounset -o pipefail

helm_e2e::worker_container() {
  printf '%s\n' "${WORKER_CONTAINER:-${KIND_CLUSTER_NAME}-worker}"
}

helm_e2e::snapshotter_selector() {
  printf 'app.kubernetes.io/name=nydus-snapshotter,app.kubernetes.io/instance=%s\n' "${HELM_RELEASE}"
}

helm_e2e::expected_snapshotter_count() {
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | grep -Ev 'control-plane|master' | wc -l | tr -d ' '
}

helm_e2e::assert_workload_healthy() {
  local timeout="${1:-180s}"
  local worker_container

  worker_container="$(helm_e2e::worker_container)"
  kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=condition=Ready -n "${WORKLOAD_NAMESPACE}" pod/"${NGINX_POD_NAME}" --timeout="${timeout}"
  docker exec "${worker_container}" sh -lc "curl -fsS -o /dev/null -I http://127.0.0.1:${NGINX_HOST_PORT}"
}

helm_e2e::get_snapshotter_active_pod_count() {
  local selector="${1:-$(helm_e2e::snapshotter_selector)}"

  kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" --no-headers 2>/dev/null | awk '$3 != "Completed" && $3 != "Succeeded" && $3 != "Failed" && $3 != "Terminating" {count++} END {print count+0}'
}

helm_e2e::dump_worker_containerd_state() {
  local worker_container

  worker_container="$(helm_e2e::worker_container)"
  docker exec "${worker_container}" sh -lc '
    echo "--- /etc/containerd/config.toml ---"
    sed -n "1,240p" /etc/containerd/config.toml || true
    echo "--- /etc/containerd/nydus-proxy.toml ---"
    sed -n "1,240p" /etc/containerd/nydus-proxy.toml || true
    echo "--- crictl info ---"
    crictl info || true
    echo "--- containerd config dump ---"
    containerd config dump 2>/dev/null || true
    echo "--- containerd journal ---"
    journalctl -u containerd -n 120 --no-pager || true
  ' >&2 || true
}

helm_e2e::dump_snapshotter_stop_debug() {
  local selector
  local worker_container

  selector="$(helm_e2e::snapshotter_selector)"
  worker_container="$(helm_e2e::worker_container)"

  kubectl --context "kind-${KIND_CLUSTER_NAME}" get ds nydus-snapshotter -n "${HELM_NAMESPACE}" -o wide >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" describe ds nydus-snapshotter -n "${HELM_NAMESPACE}" >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" -o wide >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" logs -n "${HELM_NAMESPACE}" -l app.kubernetes.io/name=nydus-snapshotter --all-containers >&2 || true
  docker exec "${worker_container}" sh -lc "ps -ef | grep containerd-nydus-grpc | grep -v grep || true" >&2 || true
}

helm_e2e::wait_for_worker_containerd_ready() {
  local context_label="${1:-snapshotter operation}"
  local worker_container
  local attempt=""

  worker_container="$(helm_e2e::worker_container)"
  for attempt in $(seq 1 60); do
    if docker exec "${worker_container}" sh -lc 'ctr --address /run/containerd/containerd.sock version >/dev/null 2>&1'; then
      return 0
    fi
    sleep 2
  done

  echo "worker containerd did not become ready during ${context_label}" >&2
  helm_e2e::dump_worker_containerd_state
  return 1
}

helm_e2e::restore_worker_containerd_baseline() {
  local context_label="${1:-snapshotter operation}"
  local worker_container
  local info_json=""
  local config_toml=""
  local default_runtime=""
  local root_snapshotter=""
  local import_count=""

  worker_container="$(helm_e2e::worker_container)"
  echo "Restoring worker containerd baseline to runc/overlayfs for ${context_label}" >&2
  docker exec "${worker_container}" sh -lc '
    set -eu
    config_path="/etc/containerd/config.toml"
    import_path="/etc/containerd/nydus-proxy.toml"

    if grep -qF "$import_path" "$config_path"; then
      sed -i "\|$import_path|d" "$config_path"
    fi

    rm -f /run/containerd-nydus/.containerd-restart-required
    systemctl restart containerd
  '

  helm_e2e::wait_for_worker_containerd_ready "${context_label}"

  info_json="$(docker exec "${worker_container}" sh -lc 'crictl info -o json')"
  config_toml="$(docker exec "${worker_container}" sh -lc 'cat /etc/containerd/config.toml')"
  default_runtime="$(python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("config", {}).get("containerd", {}).get("defaultRuntimeName", ""))' <<<"${info_json}")"
  root_snapshotter="$(printf '%s\n' "${config_toml}" | sed -n 's/^[[:space:]]*snapshotter[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  import_count="$(printf '%s\n' "${config_toml}" | grep -cF '/etc/containerd/nydus-proxy.toml' || true)"

  echo "worker containerd baseline for ${context_label}: defaultRuntimeName=${default_runtime:-<empty>} root.snapshotter=${root_snapshotter:-<empty>} nydusImportCount=${import_count:-<empty>}" >&2

  if [ "${default_runtime}" != "runc" ] || [ "${root_snapshotter}" != "overlayfs" ] || [ "${import_count}" != "0" ]; then
    echo "worker containerd did not restore to the expected runc/overlayfs baseline during ${context_label}" >&2
    helm_e2e::dump_worker_containerd_state
    return 1
  fi
}

helm_e2e::wait_for_snapshotter_stopped() {
  local selector
  local worker_container
  local pods=""
  local active_pods=""
  local running_processes=""

  selector="$(helm_e2e::snapshotter_selector)"
  worker_container="$(helm_e2e::worker_container)"

  if ! kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=jsonpath='{.status.desiredNumberScheduled}'=0 ds/nydus-snapshotter -n "${HELM_NAMESPACE}" --timeout=120s; then
    echo "Snapshotter DaemonSet desiredNumberScheduled did not reach 0" >&2
    helm_e2e::dump_snapshotter_stop_debug
    return 1
  fi

  if ! kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=jsonpath='{.status.currentNumberScheduled}'=0 ds/nydus-snapshotter -n "${HELM_NAMESPACE}" --timeout=120s; then
    echo "Snapshotter DaemonSet currentNumberScheduled did not reach 0" >&2
    helm_e2e::dump_snapshotter_stop_debug
    return 1
  fi

  if ! kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=jsonpath='{.status.numberReady}'=0 ds/nydus-snapshotter -n "${HELM_NAMESPACE}" --timeout=120s; then
    echo "Snapshotter DaemonSet numberReady did not reach 0" >&2
    helm_e2e::dump_snapshotter_stop_debug
    return 1
  fi

  pods="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  active_pods="$(helm_e2e::get_snapshotter_active_pod_count "${selector}")"
  running_processes="$(docker exec "${worker_container}" sh -lc "ps -ef | grep containerd-nydus-grpc | grep -v grep || true")"
  echo "snapshotter drain status: desired=0 current=0 ready=0 pods=${pods:-0} active=${active_pods:-0}" >&2

  if [ "${active_pods:-0}" != "0" ]; then
    echo "Snapshotter still has active pods after DaemonSet scaled down" >&2
    helm_e2e::dump_snapshotter_stop_debug
    return 1
  fi

  if [ -n "${running_processes}" ]; then
    echo "Snapshotter process is still running on the worker after DaemonSet scaled down" >&2
    printf '%s\n' "${running_processes}" >&2
    helm_e2e::dump_snapshotter_stop_debug
    return 1
  fi
}

helm_e2e::dump_snapshotter_debug() {
  local pod_name="${1:-}"
  local selector

  selector="$(helm_e2e::snapshotter_selector)"
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get ds nydus-snapshotter -n "${HELM_NAMESPACE}" -o wide >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" -o wide >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" describe ds nydus-snapshotter -n "${HELM_NAMESPACE}" >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get runtimeclass runc-overlayfs -o yaml >&2 || true
  if [ -n "${pod_name}" ]; then
    kubectl --context "kind-${KIND_CLUSTER_NAME}" describe pod "${pod_name}" -n "${HELM_NAMESPACE}" >&2 || true
  fi
  kubectl --context "kind-${KIND_CLUSTER_NAME}" logs -n "${HELM_NAMESPACE}" -l app.kubernetes.io/name=nydus-snapshotter --all-containers >&2 || true
  helm_e2e::dump_worker_containerd_state
}

helm_e2e::_wait_for_snapshotter_candidate() {
  local context_label="$1"
  local previous_pod="${2:-}"
  local selector=""
  local expected=""
  local candidate_pod=""
  local phase=""
  local ready=""
  local init_summary=""
  local container_summary=""
  local pod_events=""
  local attempt=""
  local pod_identity=""

  selector="$(helm_e2e::snapshotter_selector)"
  expected="$(helm_e2e::expected_snapshotter_count)"

  if [ "${expected:-0}" = "0" ]; then
    echo "Expected snapshotter pod count resolved to zero during ${context_label}" >&2
    helm_e2e::dump_snapshotter_debug
    return 1
  fi

  echo "Waiting for ${context_label} snapshotter desiredNumberScheduled=${expected}" >&2
  if ! kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=jsonpath="{.status.desiredNumberScheduled}"="${expected}" ds/nydus-snapshotter -n "${HELM_NAMESPACE}" --timeout=240s 1>&2; then
    echo "Snapshotter DaemonSet desiredNumberScheduled did not reach ${expected} during ${context_label}" >&2
    helm_e2e::dump_snapshotter_debug
    return 1
  fi

  echo "Waiting for ${context_label} snapshotter currentNumberScheduled=${expected}" >&2
  if ! kubectl --context "kind-${KIND_CLUSTER_NAME}" wait --for=jsonpath="{.status.currentNumberScheduled}"="${expected}" ds/nydus-snapshotter -n "${HELM_NAMESPACE}" --timeout=240s 1>&2; then
    echo "Snapshotter DaemonSet currentNumberScheduled did not reach ${expected} during ${context_label}" >&2
    helm_e2e::dump_snapshotter_debug
    return 1
  fi

  for attempt in $(seq 1 120); do
    candidate_pod="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" --sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | tail -n 1)"
    if [ -z "${candidate_pod}" ]; then
      echo "No snapshotter pod is visible yet during ${context_label} (attempt ${attempt}/120)" >&2
      sleep 2
      continue
    fi

    phase="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pod "${candidate_pod}" -n "${HELM_NAMESPACE}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    ready="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pod "${candidate_pod}" -n "${HELM_NAMESPACE}" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || true)"
    init_summary="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pod "${candidate_pod}" -n "${HELM_NAMESPACE}" -o jsonpath='{range .status.initContainerStatuses[*]}{.name}{" ready="}{.ready}{" waiting="}{.state.waiting.reason}{" terminated="}{.state.terminated.reason}{" exit="}{.state.terminated.exitCode}{";"}{end}' 2>/dev/null || true)"
    container_summary="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pod "${candidate_pod}" -n "${HELM_NAMESPACE}" -o jsonpath='{range .status.containerStatuses[*]}{.name}{" ready="}{.ready}{" restart="}{.restartCount}{" waiting="}{.state.waiting.reason}{" terminated="}{.state.terminated.reason}{" runningStarted="}{.state.running.startedAt}{";"}{end}' 2>/dev/null || true)"

    if [ -n "${previous_pod}" ]; then
      pod_identity="previous=${previous_pod} candidate=${candidate_pod}"
    else
      pod_identity="candidate=${candidate_pod}"
    fi
    echo "${context_label} snapshotter pod status: attempt=${attempt}/120 ${pod_identity} phase=${phase:-Unknown} ready=${ready:-Unknown} init=[${init_summary:-none}] containers=[${container_summary:-none}]" >&2
    kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" -o wide >&2 || true

    if [ "${ready}" = "True" ] && { [ -z "${previous_pod}" ] || [ "${candidate_pod}" != "${previous_pod}" ]; }; then
      printf '%s\n' "${candidate_pod}"
      return 0
    fi

    if [ $((attempt % 5)) -eq 0 ]; then
      echo "${context_label} snapshotter pod events (attempt ${attempt}/120):" >&2
      pod_events="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" describe pod "${candidate_pod}" -n "${HELM_NAMESPACE}" 2>/dev/null | sed -n '/^Events:/,$p' || true)"
      if [ -n "${pod_events}" ]; then
        printf '%s\n' "${pod_events}" >&2
        if printf '%s\n' "${pod_events}" | grep -q 'FailedCreatePodSandBox'; then
          echo "Snapshotter pod ${candidate_pod} is failing during sandbox creation during ${context_label}; aborting wait early" >&2
          helm_e2e::dump_snapshotter_debug "${candidate_pod}"
          return 1
        fi
      fi
    fi

    sleep 2
  done

  echo "Timed out waiting for snapshotter pod during ${context_label}" >&2
  helm_e2e::dump_snapshotter_debug "${candidate_pod}"
  return 1
}

helm_e2e::wait_for_snapshotter_ready() {
  helm_e2e::_wait_for_snapshotter_candidate "snapshotter recovery"
}

helm_e2e::wait_for_upgraded_snapshotter_pod() {
  local previous_pod="$1"

  helm_e2e::_wait_for_snapshotter_candidate "hot-upgrade" "${previous_pod}"
}

helm_e2e::get_snapshotter_pod() {
  local selector
  local pod=""

  selector="$(helm_e2e::snapshotter_selector)"
  pod="$(kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" --no-headers 2>/dev/null | awk '$2=="1/1" && $3=="Running" { print $1; exit }')"
  if [ -n "${pod}" ]; then
    printf '%s\n' "${pod}"
    return 0
  fi

  echo "Unable to find a running nydus-snapshotter pod" >&2
  kubectl --context "kind-${KIND_CLUSTER_NAME}" get pods -n "${HELM_NAMESPACE}" -l "${selector}" -o wide >&2 || true
  kubectl --context "kind-${KIND_CLUSTER_NAME}" describe ds nydus-snapshotter -n "${HELM_NAMESPACE}" >&2 || true
  return 1
}

helm_e2e::get_image_git_commit() {
  local image_tag="$1"

  docker run --rm --entrypoint /nydus-static/nydusd "${IMAGE_REPOSITORY}/${IMAGE_NAME}:${image_tag}" --version | sed -n 's/^Git Commit:[[:space:]]*//p' | head -n 1
}

helm_e2e::get_live_daemon_git_commit() {
  local worker_container
  local daemons_json
  local api_sock
  local daemon_json

  worker_container="$(helm_e2e::worker_container)"
  daemons_json="$(docker exec "${worker_container}" sh -lc "curl -fsS --unix-socket /run/containerd-nydus/system.sock http://unix/api/v1/daemons")"
  api_sock="$(python3 -c 'import json,sys; data=json.load(sys.stdin); print(next((item.get("api_socket", "") for item in data if item.get("instances")), ""))' <<<"${daemons_json}")"
  if [ -z "${api_sock}" ]; then
    echo "Unable to find a live nydusd API socket" >&2
    echo "${daemons_json}" >&2
    return 1
  fi

  daemon_json="$(docker exec "${worker_container}" sh -lc "curl -fsS --unix-socket '${api_sock}' http://unix/api/v1/daemon")"
  python3 -c 'import json,sys; data=json.load(sys.stdin); print(data.get("version", {}).get("git_commit", ""))' <<<"${daemon_json}"
}

helm_e2e::retry_until_match() {
  local description="$1"
  local expected="$2"
  local attempts="$3"
  local interval_seconds="$4"
  local callback="$5"
  local observed=""

  for _ in $(seq 1 "${attempts}"); do
    observed="$("${callback}" || true)"
    if [ "${observed}" = "${expected}" ]; then
      printf '%s\n' "${observed}"
      return 0
    fi
    sleep "${interval_seconds}"
  done

  echo "Timed out waiting for ${description} ${expected}, last observed ${observed:-<empty>}" >&2
  return 1
}

helm_e2e::wait_for_live_daemon_git_commit() {
  helm_e2e::retry_until_match "live nydusd git commit" "$1" 30 2 helm_e2e::get_live_daemon_git_commit
}
