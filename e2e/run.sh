#!/usr/bin/env bash
# Per-protocol pull-through matrix: one tool pod per protocol. The client's
# package manager is pointed STRAIGHT at the artifact Service's explicit URL
# (/artifacts/<proto>/...); there is no intercepting sidecar, no MITM CA and no
# DNS spoofing. artifact itself does the upstream fetch (through the cluster
# proxy, see HTTP_PROXY on the Deployment).
#
# For each protocol we assert:
#   1. the client's package manager installs/downloads a package from artifact;
#   2. the artifact CAS grew (proof the bytes were cached locally).
#
# Per-protocol client commands live in ./scripts/<proto>.sh and are mounted
# into the tool container at /scripts. Each script reads EASYBLOBS_BASE (the
# explicit mount, e.g. http://artifact-npm.temp.svc.cluster.local/artifacts/npm)
# and points its client at it.
#
# Usage:
#   ./run.sh                       # all protocols (default list)
#   PROTOCOLS="nuget rubygems" ./run.sh
#   KEEP=1 PROTOCOLS=helm ./run.sh # keep the pod for debugging
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="${NS:-temp}"
TOOL_IMAGE_PREFIX="${TOOL_IMAGE_PREFIX:-forgejo.develop.10.199.64.20.nip.io/easy-platform/tool}"
TOOL_TAG="${TOOL_TAG:-latest}"
# Each protocol uses its dedicated client image (toolchain/e2e: one image per
# protocol, built from official/prebuilt tarballs under /opt).
PROTOCOLS=(${PROTOCOLS:-npm pypi go cargo maven nuget rubygems composer hex pub helm conan swift conda nix huggingface protobuf debian apk rpm oci git ivy hackage cran cpan luarocks juliapkg google gradle clojars spring jitpack jsrnpm opam stackage bazel jenkins gitlfs netcache})
KEEP="${KEEP:-0}"
# JOBS is the number of protocols exercised concurrently (each has its own
# pod, so they are independent). JOBS=1 reproduces the old serial behavior.
JOBS="${JOBS:-6}"

source "${HERE}/parallel.sh"

# proto_mount maps an e2e row onto the artifact adapter that serves it (the
# maven-layout mirrors reuse the maven adapter; jsrnpm reuses npm).
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

# proto_svc returns the artifact Service host for a row (per-protocol topology,
# or the one unified Service when UNIFIED_SVC is set).
proto_svc() {
  local p="$1"
  if [ -n "${UNIFIED_SVC:-}" ]; then
    echo "${UNIFIED_SVC}.${NS}.svc.cluster.local"
  else
    echo "artifact-${p}.${NS}.svc.cluster.local"
  fi
}

# proto_path returns the path under /artifacts for a row: the protocol mount,
# or the reserved-segment form for the maven-layout mirrors / JSR npm-compat
# (a named target is not a mount; reach it via /artifacts/<proto>/_upstream/<host>).
proto_path() {
  case "$1" in
    google)  echo "maven/_upstream/dl.google.com" ;;
    gradle)  echo "maven/_upstream/plugins.gradle.org" ;;
    clojars) echo "maven/_upstream/repo.clojars.org" ;;
    spring)  echo "maven/_upstream/repo.spring.io" ;;
    jitpack) echo "maven/_upstream/jitpack.io" ;;
    jsrnpm)  echo "npm/_upstream/npm.jsr.io" ;;
    *)       proto_mount "$1" ;;
  esac
}

# proto_base returns the explicit mount base the client must be pointed at:
# http://<svc>/artifacts/<path>. git/ivy/netcache append the upstream host to
# this (the host is part of their path).
proto_base() {
  local p="$1"
  echo "http://$(proto_svc "$p")/artifacts/$(proto_path "$p")"
}

# proto_img maps a row to its client image.
proto_img() {
  local p="$1"
  case "$p" in
    google|gradle|clojars|spring|jitpack) echo "${TOOL_IMAGE_PREFIX}-maven:${TOOL_TAG}" ;;
    jsrnpm)  echo "${TOOL_IMAGE_PREFIX}-npm:${TOOL_TAG}" ;;
    jsr)     echo "${TOOL_IMAGE_PREFIX}-haskell:${TOOL_TAG}" ;;
    gitlfs)  echo "${TOOL_IMAGE_PREFIX}-gitlfs:${TOOL_TAG}" ;;
    opam|stackage|bazel|jenkins) echo "${TOOL_IMAGE_PREFIX}-haskell:${TOOL_TAG}" ;;
    netcache) echo "${TOOL_IMAGE_PREFIX}-git:${TOOL_TAG}" ;;
    rpm|apk|nix) echo "${TOOL_IMAGE_PREFIX}-$p:latest" ;;
    *)       echo "${TOOL_IMAGE_PREFIX}-$p:${TOOL_TAG}" ;;
  esac
}

# cas_count prints how many blobs the artifact pod holds.
cas_count() {
  local dep="deploy/artifact-$1"
  [ -n "${UNIFIED_SVC:-}" ] && dep="deploy/${UNIFIED_SVC}"
  # `system` has no upstream and therefore no CAS growth assertion; other
  # protocols count blobs (the git mirror keeps its objects on disk, not in
  # the CAS, so it is excluded too).
  case "$1" in system|git) echo 0; return ;; esac
  kubectl exec -n "$NS" "$dep" -- sh -c 'ls /data/blobs/sha256/*/* 2>/dev/null | wc -l' 2>/dev/null | tr -d '[:space:]'
}

# git_mirror_count counts the bare mirrors the git protocol has stored; it is
# the git analogue of cas_count.
git_mirror_count() {
  local dep="deploy/artifact-$1"
  [ -n "${UNIFIED_SVC:-}" ] && dep="deploy/${UNIFIED_SVC}"
  kubectl exec -n "$NS" "$dep" -- sh -c 'find /data/git -name HEAD 2>/dev/null | wc -l' 2>/dev/null | tr -d '[:space:]'
}

write_script_cm() {
  local p="$1" name="pull-$1"
  kubectl create configmap "${name}-script" -n "$NS" \
    --from-file="${p}.sh=${HERE}/scripts/${p}.sh" \
    --dry-run=client -o yaml | kubectl apply -n "$NS" -f - >/dev/null
}

write_pod() {
  local p="$1" name="pull-$1"
  local img base
  img="$(proto_img "$p")"
  base="$(proto_base "$p")"
  cat <<YAML | kubectl apply -n "$NS" -f - >/dev/null
apiVersion: v1
kind: Pod
metadata: { name: ${name}, labels: { app: pull } }
spec:
  containers:
  - name: tool
    image: ${img}
    # Always pull: the tool images are re-pushed under the same tag as the
    # matrix evolves, and IfNotPresent would silently run a stale cached layer.
    imagePullPolicy: Always
    command: ["sleep", "1800"]
    resources:
      requests: { cpu: 100m, memory: 256Mi }
      limits:   { cpu: "2", memory: 4Gi }
    env:
    # The explicit mount the client is pointed at. git/ivy/netcache append the
    # upstream host themselves.
    - { name: EASYBLOBS_BASE, value: "${base}" }
    - { name: EASYBLOBS_URL, value: "http://$(proto_svc "$p")" }
    volumeMounts:
    - { name: script, mountPath: /scripts, readOnly: true }
  volumes:
  - { name: script, configMap: { name: ${name}-script } }
YAML
}

run_one() {
  local p="$1" name="pull-$1" before after growth out rc before_git
  before="$(cas_count "$p")"
  before_git="$(git_mirror_count "$p" 2>/dev/null || echo 0)"
  write_script_cm "$p"
  kubectl delete pod -n "$NS" "$name" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
  write_pod "$p"

  if ! kubectl wait -n "$NS" --for=condition=Ready "pod/${name}" --timeout=300s >/dev/null 2>&1; then
    echo "FAIL $p  (pod not ready)"
    emit_result "$p" "FAIL $p pod-not-ready"
    kubectl logs -n "$NS" "$name" -c tool --tail=20 2>&1 | sed 's/^/      | /'
    return
  fi
  out="$(kubectl exec -n "$NS" "$name" -c tool -- timeout 600 sh "/scripts/${p}.sh" 2>&1)"; rc=$?
  after="$(cas_count "$p")"; growth=$(( ${after:-0} - ${before:-0} ))
  if [ "$p" = "git" ]; then
    # git stores a bare mirror on disk rather than in the CAS.
    growth="$(git_mirror_count "$p")"
    growth=$(( ${growth:-0} - ${before_git:-0} ))
  fi
  if [ "$rc" -eq 0 ] && [ "${growth:-0}" -gt 0 ]; then
    echo "PASS $p  (CAS +$growth)"
    emit_result "$p" "PASS $p (CAS +$growth)"
  elif [ "$rc" -eq 0 ]; then
    echo "FAIL $p  (client ok but CAS did not grow — not cached)"
    emit_result "$p" "FAIL $p not-cached"
    kubectl logs -n "$NS" "$name" -c tool --tail=8 2>&1 | sed 's/^/      | /'
  else
    echo "FAIL $p  (client rc=$rc)"
    emit_result "$p" "FAIL $p rc=$rc"
    echo "$out" | tail -12 | sed 's/^/      | /'
  fi
  [ "$KEEP" = "1" ] || kubectl delete pod -n "$NS" "$name" --ignore-not-found >/dev/null 2>&1
}

run_pool run_one "${PROTOCOLS[@]}"
