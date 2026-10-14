#!/usr/bin/env bash
# One-command regression for the artifact registry: build a single image and
# exercise it BOTH ways, plus the auxiliary matrices.
#
#   bash test-all.sh                 # unified + per-protocol + lifecycle + search
#   bash test-all.sh unified         # one service, all protocols concurrent
#   bash test-all.sh perproto        # one service per protocol (isolation)
#   bash test-all.sh lifecycle       # publish/upgrade/delete per protocol
#   bash test-all.sh search          # search/aux endpoints
#
# The unified topology is the production shape (easy-platform runs one instance for
# every protocol); the per-protocol topology isolates each adapter so a failure
# cannot be masked by a sibling. Both point the client's package manager
# STRAIGHT at the artifact Service (explicit /artifacts/<proto>/ URLs) — there
# is no intercepting sidecar — and assert the install succeeded AND the CAS
# grew, so "fetched once" is real.
#
# The suite scripts are invoked through `bash` so the harness does not depend
# on the executable bit (repo files are checked out non-executable).
#
# Env:
#   NS                 namespace (default temp)
#   IMAGE              artifact image (default the pinned release below)
#   JOBS               protocols exercised concurrently (default 8)
#   PROTOCOLS          override the matrix (space separated)
#   SUITES             override which suites run (space separated)
#   KEEP               "1" keeps the deployments after the run (default: reap)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="${NS:-temp}"
IMAGE="${IMAGE:-forgejo.develop.10.199.64.20.nip.io/easy-platform/artifact:v0.28.0}"
export NS IMAGE JOBS="${JOBS:-8}"
# Keep the deployments after the run for debugging (default: tear them down).
KEEP="${KEEP:-0}"

SUITES="${SUITES:-${*:-unified perproto lifecycle search}}"

# The suite manifests (deploy-unified/deploy-protocol/lifecycle/search) create
# Deployments + Services that were previously left behind on every run. Remove
# them on exit unless KEEP=1, so a regression run does not accumulate resources.
cleanup() {
  [ "$KEEP" = "1" ] && return 0
  kubectl -n "$NS" delete deploy,svc -l app=artifact-e2e --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl -n "$NS" delete deploy,svc -l app=artifact-unified --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

run_unified() {
  echo "########## unified: ONE service, all protocols concurrent ##########"
  NS="$NS" IMAGE="$IMAGE" bash "${HERE}/deploy-unified.sh" >/dev/null
  kubectl -n "$NS" rollout status deploy/artifact-unified --timeout=180s >/dev/null
  UNIFIED_SVC=artifact-unified NS="$NS" JOBS="$JOBS" bash "${HERE}/run.sh"
}

run_perproto() {
  echo "########## per-protocol: one service per protocol (isolation) ##########"
  NS="$NS" IMAGE="$IMAGE" bash "${HERE}/deploy-protocol.sh" >/dev/null
  NS="$NS" JOBS="$JOBS" bash "${HERE}/run.sh"
}

run_lifecycle() {
  echo "########## lifecycle: publish -> public -> private -> upgrade -> delete ##########"
  NS="$NS" IMAGE="$IMAGE" bash "${HERE}/lifecycle/run.sh"
}

run_search() {
  echo "########## search/aux matrix ##########"
  NS="$NS" bash "${HERE}/run-search.sh"
}

rc=0
for s in $SUITES; do
  case "$s" in
    unified)   run_unified   || rc=1 ;;
    perproto)  run_perproto  || rc=1 ;;
    lifecycle) run_lifecycle || rc=1 ;;
    search)    run_search    || rc=1 ;;
    *) echo "unknown suite: $s (unified|perproto|lifecycle|search)"; rc=1 ;;
  esac
done
exit "$rc"
