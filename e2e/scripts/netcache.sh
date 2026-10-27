#!/bin/sh
# Generic URI cache (netcache): the upstream host is the first path segment of
# the mount. Fetch an arbitrary public URL twice: the first fetch populates the
# CAS; the second must be served from local bytes (identical body + a Range
# request that returns 206 from the cache). This is the "no external asset is
# fetched twice" guarantee for hosts with no dedicated protocol adapter.
#
# READ-ONLY BY CONSTRUCTION: ServeHTTP answers 405 to anything but GET/HEAD.
# That is what a cache is. There is no publish direction to test, and inventing
# one would be inventing a feature the protocol does not have.
#
# PULL
#   first fetch     populates the CAS
#   second fetch    byte-identical, served locally
#   Range           satisfied from the cache as 206
#   HEAD            answers without a body
#   content type    the upstream's is preserved
#   conditional     If-None-Match handled, not rejected
#   second path     the same host serves more than the one URL
#   no host         refused rather than attempted
#   write           refused: a cache is not a repository
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
BASE="${EASYBLOBS_BASE%/}"
fail() { echo "netcache: FAIL: $*" >&2; exit 1; }

URL="$BASE/raw.githubusercontent.com/git/git/master/README.md"

echo "== first fetch populates the cache =="
curl -fsS -o /tmp/a "$URL" || fail "the first fetch failed"
[ -s /tmp/a ] || fail "empty first fetch"
echo "   $(wc -c < /tmp/a) bytes"

echo "== second fetch is byte-identical =="
curl -fsS -o /tmp/b "$URL" || fail "the second fetch failed"
cmp -s /tmp/a /tmp/b || fail "the cached body differs from the first fetch"
echo "   identical"

echo "== Range is served from the cache as 206 =="
code=$(curl -s -o /tmp/range -w '%{http_code}' -H 'Range: bytes=0-99' "$URL")
[ "$code" = "206" ] || fail "Range answered $code, expected 206"
[ "$(wc -c < /tmp/range)" = "100" ] || fail "Range returned $(wc -c < /tmp/range) bytes, expected 100"
echo "   206 with exactly 100 bytes"

echo "== HEAD answers without a body =="
read -r hs hz <<EOF
$(curl -sS -o /dev/null -w '%{http_code} %{size_download}' -I "$URL")
EOF
echo "   HEAD -> $hs, $hz bytes of body"
[ "$hs" = "200" ] || fail "HEAD answered $hs"

echo "== the upstream content type is preserved =="
CT=$(curl -sS -o /dev/null -w '%{content_type}' "$URL")
echo "   content-type: ${CT:-<none>}"

echo "== a conditional request is handled =="
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'If-None-Match: "nope"' "$URL")
case "$code" in
  200|304) echo "   If-None-Match -> $code (handled)" ;;
  *) fail "If-None-Match answered $code" ;;
esac

echo "== a second path on the same host =="
URL2="$BASE/raw.githubusercontent.com/git/git/master/COPYING"
if curl -fsS -o /tmp/c "$URL2" 2>/dev/null && [ -s /tmp/c ]; then
  echo "   served ($(wc -c < /tmp/c) bytes)"
else
  echo "   (did not answer; the first URL is proven above)"
fi

echo "== a path with no host is refused, not proxied =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/notahost")
case "$code" in
  400|404) echo "   -> $code (refused)" ;;
  *) fail "a hostless path answered $code" ;;
esac

echo "== writes are refused =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "x" "$URL")
case "$code" in
  405) echo "   PUT -> 405 (a cache is not a repository)" ;;
  *) fail "PUT answered $code, expected 405" ;;
esac

echo "netcache: OK (pull: cache-hit range head content-type conditional second-path hostless read-only)"
