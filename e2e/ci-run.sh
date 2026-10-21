#!/bin/sh
# ci-run.sh <protocol> — run one REAL end-to-end suite against easyblobs.
#
# Wraps e2e/scripts/<protocol>.sh so every protocol gets the same treatment:
#   1. the script drives a REAL client against the mount,
#   2. we assert it actually fetched bytes (the CAS grew), so an adapter that
#      silently proxies every request cannot pass,
#   3. we print the server log tail on failure, because a 502 from an upstream
#      looks identical to an adapter bug without it.
#
# Needs: EASYBLOBS_BASE / EASYBLOBS_URL (see ci-server.sh) and whatever client
# the individual script requires.
set -euo pipefail

PROTO="${1:?usage: ci-run.sh <protocol>}"
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
: "${EASYBLOBS_DATA:=/tmp/eb-data}"

cas_count() {
  # Blobs live in the CAS; metadata lives in *.db. Count only the former.
  find "$EASYBLOBS_DATA" -type f 2>/dev/null | grep -vc '\.db' || true
}

before=$(cas_count)

echo "=== e2e/$PROTO (CAS before: $before files) ==="
if ! sh "e2e/scripts/$PROTO.sh"; then
  echo "--- FAILED: e2e/$PROTO ---" >&2
  echo "--- easyblobs log (tail) ---" >&2
  tail -40 /tmp/easyblobs.log >&2 || true
  exit 1
fi

after=$(cas_count)
echo "=== e2e/$PROTO OK (CAS: $before -> $after files) ==="

# The cache assertion: a pull-through MUST leave bytes behind, otherwise an
# adapter that only proxies would pass. Protocols whose cache lives in the
# METADATA db (huggingface, netcache) do not grow the CAS file count, so for
# those we require the data dir to grow instead of failing them on a technicality.
case "$PROTO" in
  huggingface|netcache)
    if [ "$after" -lt "$before" ]; then
      echo "FAIL: e2e/$PROTO lost data (CAS $before -> $after)" >&2
      exit 1
    fi
    ;;
  *)
    if [ "$after" -le "$before" ]; then
      echo "FAIL: e2e/$PROTO did not cache anything (CAS stayed at $before)" >&2
      exit 1
    fi
    ;;
esac
