#!/bin/sh
# ci-run.sh <protocol> — run ONE real end-to-end suite against easyblobs.
#
# Wraps e2e/scripts/<protocol>.sh so every protocol gets identical treatment:
#   1. the script drives a REAL client against the mount,
#   2. we assert it actually fetched bytes, so an adapter that only proxies
#      every request cannot pass,
#   3. we print the server log tail on failure, because a 502 from an upstream
#      looks exactly like an adapter bug without it.
#
# EACH PROTOCOL HAS ITS OWN MOUNT. Some also live under an explicit UPSTREAM
# segment, and e2e/run.sh already encodes that mapping in proto_path(). We
# mirror it here so every client is pointed at the exact prefix it needs:
#
#   helm      -> /artifacts/helm
#   google    -> /artifacts/maven/_upstream/dl.google.com
#   gradle    -> /artifacts/maven/_upstream/plugins.gradle.org
#   clojars   -> /artifacts/maven/_upstream/repo.clojars.org
#   spring    -> /artifacts/maven/_upstream/repo.spring.io
#   jitpack   -> /artifacts/maven/_upstream/jitpack.io
#   jsrnpm    -> /artifacts/npm/_upstream/npm.jsr.io
#   gitlfs    -> /artifacts/git
#   others    -> /artifacts/<protocol>
#
# Needs: EASYBLOBS_ORIGIN (e.g. http://127.0.0.1:8080) plus whatever client the
# individual script requires.
set -euo pipefail

PROTO="${1:?usage: ci-run.sh <protocol>}"
: "${EASYBLOBS_ORIGIN:?EASYBLOBS_ORIGIN not set}"
: "${EASYBLOBS_DATA:=/tmp/eb-data}"

# proto_mount mirrors e2e/run.sh: the /artifacts/<x> a protocol lives under.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

# proto_path mirrors e2e/run.sh: the explicit upstream segment, when present.
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

# The mount THIS protocol's client must be pointed at.
EASYBLOBS_BASE="${EASYBLOBS_ORIGIN%/}/artifacts/$(proto_path "$PROTO")"
export EASYBLOBS_BASE
# OCI is spec-fixed at /v2 on the origin, so its script reads EASYBLOBS_URL.
EASYBLOBS_URL="${EASYBLOBS_ORIGIN%/}"
export EASYBLOBS_URL

cas_count() {
  # Blobs live in the CAS; metadata lives in *.db. Count only the former.
  find "$EASYBLOBS_DATA" -type f 2>/dev/null | grep -vc '\.db' || true
}

before=$(cas_count)

echo "=== e2e/$PROTO (base=$EASYBLOBS_BASE, CAS before: $before files) ==="
if ! sh "e2e/scripts/$PROTO.sh"; then
  echo "--- FAILED: e2e/$PROTO ---" >&2
  echo "--- easyblobs log (tail) ---" >&2
  tail -40 /tmp/easyblobs.log >&2 || true
  exit 1
fi

after=$(cas_count)
echo "=== e2e/$PROTO OK (CAS: $before -> $after files) ==="

# The cache assertion: a pull-through MUST leave bytes behind, else an adapter
# that only proxies would pass. A couple of protocols keep their cache in the
# METADATA db and never grow the CAS file count, so they assert "did not lose
# data" instead of a strict increase.
case "$PROTO" in
  huggingface|netcache)
    [ "$after" -ge "$before" ] || {
      echo "FAIL: e2e/$PROTO lost data (CAS $before -> $after)" >&2
      exit 1
    }
    ;;
  *)
    [ "$after" -gt "$before" ] || {
      echo "FAIL: e2e/$PROTO did not cache anything (CAS stayed at $before)" >&2
      exit 1
    }
    ;;
esac
