#!/usr/bin/env bash
set -euo pipefail

# Minimal repro for: idmap commit image verifies OK but fails to run on k3s+containerd
# Usage:
#   bash scripts/repro_k3s_idmap_commit.sh

TS="$(date +%s)"
BASE_REF="localhost:5000/nydus/busybox:nydus-merge"
SRC_C="nydus-idmap-src-${TS}"
VERIFY_C="nydus-idmap-verify-${TS}"
TARGET_REF="localhost:5000/nydus/busybox:commit-idmap-${TS}"
LOG_DIR="/tmp/nydus-repro-${TS}"
MNT_PATH="/tmp/nydus-mnt-${TS}"

mkdir -p "${LOG_DIR}" "${MNT_PATH}"

echo "== Environment =="
date -Is
sudo systemctl is-active k3s
sudo k3s --version || true
sudo k3s ctr version || true
nydusify --version || true
containerd-nydus-grpc --version || true

cleanup() {
  sudo k3s ctr -n k8s.io t rm -f "${SRC_C}" >/dev/null 2>&1 || true
  sudo k3s ctr -n k8s.io c rm "${SRC_C}" >/dev/null 2>&1 || true
  sudo k3s ctr -n k8s.io t rm -f "${VERIFY_C}" >/dev/null 2>&1 || true
  sudo k3s ctr -n k8s.io c rm "${VERIFY_C}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! sudo k3s ctr -n k8s.io c info local-registry >/dev/null 2>&1; then
  echo "== Start local registry =="
  sudo k3s ctr -n k8s.io images pull docker.io/library/registry:2 >/dev/null
  sudo k3s ctr -n k8s.io run -d --net-host docker.io/library/registry:2 local-registry >/dev/null
fi

echo "== Ensure base image =="
if ! sudo k3s ctr images ls | grep -q "${BASE_REF}"; then
  sudo nydusify convert \
    --source docker.io/library/busybox:latest \
    --target "${BASE_REF}" \
    --merge-platform \
    --platform linux/amd64 \
    --plain-http \
    --work-dir "${LOG_DIR}/convert-work" \
    >"${LOG_DIR}/convert.log" 2>&1
fi
sudo k3s ctr images pull --plain-http "${BASE_REF}" >"${LOG_DIR}/pull-base.log" 2>&1

echo "== Run idmap source container =="
sudo k3s ctr -n k8s.io run -d --snapshotter nydus \
  --uidmap 0:100000:65536 \
  --gidmap 0:100000:65536 \
  "${BASE_REF}" \
  "${SRC_C}" \
  sh -c 'echo IDMAP_MARKER_OK > /tmp/idmap-marker; sleep 3600'

sudo k3s ctr -n k8s.io t exec --exec-id check "${SRC_C}" sh -c 'id; cat /tmp/idmap-marker' >"${LOG_DIR}/source-check.log" 2>&1

echo "== Commit idmap container to nydus image =="
sudo nydusify commit \
  --containerd-address /run/k3s/containerd/containerd.sock \
  --namespace k8s.io \
  --container "${SRC_C}" \
  --target "${TARGET_REF}" \
  --source-insecure \
  --target-insecure \
  >"${LOG_DIR}/commit.log" 2>&1

echo "== Validate committed image (nydusify check) =="
nydusify check --target "${TARGET_REF}" --target-insecure >"${LOG_DIR}/check.log" 2>&1

echo "== Try to run committed image (expected failure) =="
set +e
sudo k3s ctr images pull --plain-http "${TARGET_REF}" >"${LOG_DIR}/pull-commit.log" 2>&1
PULL_RC=$?
sudo k3s ctr -n k8s.io run --rm --snapshotter nydus "${TARGET_REF}" "${VERIFY_C}" sh -c 'cat /tmp/idmap-marker' >"${LOG_DIR}/run-commit.log" 2>&1
RUN_RC=$?
set -e

echo "== Mount committed image and read marker (expected success) =="
set +e
sudo nydusify mount --target "${TARGET_REF}" --target-insecure --mount-path "${MNT_PATH}" --work-dir "${LOG_DIR}/mount-work" >"${LOG_DIR}/mount.log" 2>&1 &
MPID=$!
MOUNT_READ_RC=1
for _ in $(seq 1 30); do
  if sudo test -f "${MNT_PATH}/tmp/idmap-marker"; then
    sudo cat "${MNT_PATH}/tmp/idmap-marker" >"${LOG_DIR}/mount-marker.log"
    MOUNT_READ_RC=0
    break
  fi
  sleep 1
done
sudo kill "${MPID}" >/dev/null 2>&1 || true
wait "${MPID}" >/dev/null 2>&1 || true
set -e

echo

echo "== Summary =="
echo "TARGET_REF=${TARGET_REF}"
echo "PULL_RC=${PULL_RC}"
echo "RUN_RC=${RUN_RC}"
echo "MOUNT_READ_RC=${MOUNT_READ_RC}"

echo

echo "== Key log snippets =="
echo "[commit]"
tail -n 20 "${LOG_DIR}/commit.log" || true
echo

echo "[check]"
tail -n 20 "${LOG_DIR}/check.log" || true
echo

echo "[run committed image]"
tail -n 40 "${LOG_DIR}/run-commit.log" || true
echo

echo "[mount marker]"
cat "${LOG_DIR}/mount-marker.log" 2>/dev/null || true

echo

echo "Logs saved at: ${LOG_DIR}"
