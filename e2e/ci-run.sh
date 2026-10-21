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

# The cache assertion: a pull-through MUST leave bytes behind. A protocol that
# legitimately serves nothing from the CAS (netcache stores raw HTTP bodies in
# its own table) still grows the data dir, so a strictly-greater count is the
# right bar for those; for the rest it proves the artifact was really cached.
if [ "$after" -le "$before" ]; then
  echo "FAIL: e2e/$PROTO did not cache anything (CAS stayed at $before)" >&2
  exit 1
fi
