#!/bin/sh
# Generic URI cache (netcache): the upstream host is the first path segment of
# the mount. Fetch an arbitrary public URL twice: the first fetch populates the
# CAS; the second must be served from local bytes (identical body + a Range
# request that returns 206 from the cache). This is the "no external asset is
# fetched twice" guarantee for hosts with no dedicated protocol adapter.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
URL="$EASYBLOBS_BASE/raw.githubusercontent.com/git/git/master/README.md"

curl -fsS -o /tmp/a "$URL"
[ -s /tmp/a ] || { echo "netcache: empty first fetch"; exit 1; }

curl -fsS -o /tmp/b "$URL"
cmp -s /tmp/a /tmp/b || { echo "netcache: cached body differs"; exit 1; }

# A Range request must be satisfied locally (206) without a second upstream
# fetch; the CAS serves byte ranges.
code="$(curl -s -o /tmp/range -w '%{http_code}' -H 'Range: bytes=0-99' "$URL")"
[ "$code" = "206" ] || { echo "netcache: range rc=$code"; exit 1; }
[ "$(wc -c </tmp/range)" = "100" ] || { echo "netcache: range size wrong"; exit 1; }

echo "netcache: arbitrary URL cached, range served locally ($(wc -c </tmp/a) bytes)"
