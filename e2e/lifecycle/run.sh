#!/usr/bin/env bash
# Full package-lifecycle matrix: for each protocol, one tool pod walks five
# stages with a REAL client —
#   1 publish   A publishes a uniquely-named private package (v1)
#   2 public    B installs a well-known PUBLIC package (pull-through still
#               works alongside the private content)
#   3 private   B (fresh caches) installs A's private package v1
#   4 upgrade   A publishes v2, B consumes it; both versions visible
#   5 delete    the package is deleted (unpublish / unlist / yank — whatever
#               the protocol supports) and B's install now fails
# A/B are separate processes with isolated HOMEs/caches in the same pod.
#
# There is no intercepting sidecar: the client is pointed straight at the
# artifact Service's explicit URL. EASYBLOBS_BASE is the protocol mount
# (http://<svc>/artifacts/<proto>); hosted publishes use <base>/_hosted/<repo>/.
#
# Usage:
#   ./run.sh                          # all lifecycle protocols
#   PROTOCOLS="npm oci" ./run.sh
#   KEEP=1 PROTOCOLS=pypi ./run.sh    # keep the pod for debugging
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="${NS:-temp}"
TOOL_IMAGE_PREFIX="${TOOL_IMAGE_PREFIX:-forgejo.develop.10.199.64.20.nip.io/easy-platform/tool}"
TOOL_TAG="${TOOL_TAG:-latest}"
# Real-client protocols first; extend the list as stage scripts are added.
PROTOCOLS=(${PROTOCOLS:-npm pypi cargo maven nuget rubygems pub conan oci hex swift helm composer go debian apk rpm conda})
KEEP="${KEEP:-0}"
V1="${V1:-1.0.0}"
V2="${V2:-2.0.0}"
STAGES=(publish public private upgrade delete)
# JOBS protocols run concurrently (each has its own pod). JOBS=1 is serial.
JOBS="${JOBS:-6}"

source "${HERE}/../parallel.sh"

# proto_mount maps a row to the artifact adapter that serves it.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

proto_svc() {
  local p="$1"
  if [ -n "${UNIFIED_SVC:-}" ]; then
    echo "${UNIFIED_SVC}.${NS}.svc.cluster.local"
  else
    echo "artifact-${p}.${NS}.svc.cluster.local"
  fi
}

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

# Per-protocol fully-qualified package name for run SUFFIX.
proto_pkg() {
  case "$1" in
    npm)      echo "@lc${SUFFIX}/probe" ;;
    pub)      echo "lc_probe_${SUFFIX}" ;;
    nuget)    echo "Lc.Probe.${SUFFIX}" ;;
    *)        echo "lc-probe-${SUFFIX}" ;;
  esac
}

write_script_cm() {
  local p="$1" name="lc-$1"
  kubectl create configmap "${name}-script" -n "$NS" \
    --from-file="${p}.sh=${HERE}/scripts/${p}.sh" \
    --dry-run=client -o yaml | kubectl apply -n "$NS" -f - >/dev/null
}

write_pod() {
  local p="$1" name="lc-$1" img
  img="$(proto_img "$p")"
  cat <<YAML | kubectl apply -n "$NS" -f - >/dev/null
apiVersion: v1
kind: Pod
metadata: { name: ${name}, labels: { app: lc } }
spec:
  containers:
  - name: tool
    image: ${img}
    imagePullPolicy: Always
    command: ["sleep", "3600"]
    resources:
      requests: { cpu: 100m, memory: 256Mi }
      limits:   { cpu: "2", memory: 4Gi }
    env:
    - { name: EASYBLOBS_BASE, value: "http://$(proto_svc "$p")/artifacts/$(proto_mount "$p")" }
    - { name: EASYBLOBS_URL, value: "http://$(proto_svc "$p")" }
    volumeMounts:
    - { name: script, mountPath: /scripts, readOnly: true }
  volumes:
  - { name: script, configMap: { name: ${name}-script } }
YAML
}

# run_stage <pod> <proto> <stage> <ver>: fresh HOME/WORK per stage so every
# stage is a cold client. Prints the exec output, returns its rc.
run_stage() {
  local name="$1" p="$2" stage="$3" ver="$4"
  kubectl exec -n "$NS" "$name" -c tool -- env \
    STAGE="$stage" SUFFIX="$SUFFIX" NAME="$(proto_pkg "$p")" \
    V1="$V1" V2="$V2" VER="$ver" \
    HOME="/tmp/lc-home-${stage}" WORK="/tmp/lc-work-${stage}" \
    sh -c 'rm -rf "$HOME" "$WORK"; mkdir -p "$HOME" "$WORK"; sh "/scripts/'"$p"'.sh"'
}

run_one() {
  local p="$1" name="lc-$1" stage ver rc out stage_result
  local failed=0
  SUFFIX="$(date +%s%N)"
  local pkg; pkg="$(proto_pkg "$p")"

  write_script_cm "$p"
  kubectl delete pod -n "$NS" "$name" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
  write_pod "$p"
  if ! kubectl wait -n "$NS" --for=condition=Ready "pod/${name}" --timeout=300s >/dev/null 2>&1; then
    echo "FAIL $p (pod not ready)"
    emit_result "$p" "FAIL $p pod-not-ready"
    kubectl logs -n "$NS" "$name" -c tool --tail=20 2>&1 | sed 's/^/      | /'
    return
  fi

  printf '%-9s %s\n' "$p" "$pkg"
  for stage in "${STAGES[@]}"; do
    # publish publishes v1; upgrade publishes v2 then consumes it; the other
    # stages consume/verify.
    ver="$V1"; [ "$stage" = "upgrade" ] && ver="$V2"
    out="$(run_stage "$name" "$p" "$stage" "$ver" 2>&1)"; rc=$?
    if [ "$rc" -eq 0 ]; then
      stage_result="PASS"; echo "  PASS ${stage}"
    else
      stage_result="FAIL"; failed=1; echo "  FAIL ${stage} (rc=$rc)"
      printf '%s\n' "$out" | tail -8 | sed 's/^/      | /'
      # A failed stage invalidates the rest of the chain — best-effort remove
      # the package so the next run starts clean, then stop this protocol.
      if [ "$stage" != "delete" ]; then
        run_stage "$name" "$p" delete "$V2" >/dev/null 2>&1 || true
      fi
      break
    fi
  done

  if [ "$failed" -eq 0 ]; then
    emit_result "$p" "PASS $p [${STAGES[*]}]"
  else
    emit_result "$p" "FAIL $p (see above)"
  fi
  [ "$KEEP" = "1" ] || kubectl delete pod -n "$NS" "$name" --ignore-not-found >/dev/null 2>&1
}

run_pool run_one "${PROTOCOLS[@]}"
