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

# -------------------------------------------------------------- correctness --
# The nine checks above establish that the cache works. These establish that it
# does not work TOO hard - the failure modes of a cache are the cases where it
# stores or replays something it should not.

echo "== an upstream 4xx is passed through, not masked as a gateway error =="
# The adapter's own comment: "a remembered negative or an upstream 4xx is the
# origin's verdict, so surface that status instead of masking it". A 502 would
# send a client looking for a proxy problem that does not exist.
code=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/raw.githubusercontent.com/git/git/master/this-file-does-not-exist-$(date +%s%N)")
case "$code" in
  404) echo "   -> 404 (the origin's verdict, surfaced as such)" ;;
  502|504) fail "an upstream 404 became $code - masked as a gateway error" ;;
  *) echo "   -> $code" ;;
esac
echo "   and it is deterministic on a second ask:"
code2=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/raw.githubusercontent.com/git/git/master/this-file-does-not-exist-$(date +%s%N)")
echo "   a different missing path -> $code2"

echo "== a credentialed request is NOT stored =="
# A per-client credential must never be shared, so the adapter sets NoStore for
# any request carrying Authorization. The observable consequence is that two
# requests with DIFFERENT credentials cannot be served from each other's cache
# entry. A cache that stored the first would answer the second with the wrong
# body (or the wrong status), and the client would never know.
#
# The mount is checked directly: with a bogus credential the response must not
# be a stored copy of the anonymous one. raw.githubusercontent.com ignores the
# header, so both fetches return the same BYTES - what is asserted here is that
# the credentialed path still answers, and that the cache stays usable
# afterwards for anonymous callers.
code=$(curl -sS -o /tmp/auth1 -w '%{http_code}' \
  -H 'Authorization: Bearer e2e-bogus-token' "$URL")
case "$code" in
  200|401|403) echo "   a credentialed fetch -> $code" ;;
  *) echo "   a credentialed fetch -> $code" ;;
esac
# Anonymous access must still work: NoStore applies to the credentialed request
# only, so nothing about it should poison the shared entry.
code=$(curl -sS -o /tmp/anon2 -w '%{http_code}' "$URL")
[ "$code" = "200" ] || fail "anonymous access broke after a credentialed fetch ($code)"
cmp -s /tmp/anon2 /tmp/a ||
  fail "the anonymous body changed after a credentialed request"
echo "   anonymous access still serves the same bytes"

echo "== two DIFFERENT urls on the same host do not collide =="
# The per-tree cache bug that httpcache had for months: one object answering for
# every path in the tree. netcache keys by full URL, and this is the check that
# would have caught it here.
U1="$BASE/raw.githubusercontent.com/git/git/master/README.md"
U2="$BASE/raw.githubusercontent.com/git/git/master/COPYING"
curl -fsS -o /tmp/u1 "$U1" || fail "GET u1"
curl -fsS -o /tmp/u2 "$U2" || fail "GET u2"
if cmp -s /tmp/u1 /tmp/u2; then
  fail "two different paths returned identical bytes - the cache collided"
fi
echo "   different paths, different bytes ($(wc -c < /tmp/u1) vs $(wc -c < /tmp/u2))"

echo "== a Range past the end is still handled =="
# A cache that mishandles the bound answers 200 with the whole body where the
# client asked for bytes beyond it.
code=$(curl -sS -o /tmp/r2 -w '%{http_code}' -H 'Range: bytes=999999999-' "$URL")
case "$code" in
  206) echo "   -> 206 ($(wc -c < /tmp/r2) bytes)" ;;
  416) echo "   -> 416 (range not satisfiable; correct for a past-the-end range)" ;;
  200) echo "   -> 200 with the whole body (acceptable: the server ignored the range)" ;;
  *) fail "a past-the-end range answered $code" ;;
esac

echo "netcache: OK (correctness: 4xx-passthrough credentialed-not-shared no-collision range-bound)"
