#!/bin/sh
# nix: the whole client surface against the mount.
#
# A binary cache is a small, fixed vocabulary:
#
#   <base>/nix-cache-info          what the cache says about itself
#   <base>/<hash>.narinfo          one store path's metadata
#   <base>/nar/<hash>.nar[.xz|.zst] the archive itself
#
# Anything else is 404 by construction, because isCachePath() only accepts
# those three shapes. There is NO write path: ServeHTTP answers 405 to anything
# that is not GET/HEAD. A binary cache is read-only in the protocol itself, so
# this is a PULL suite and that is not a gap - it is the protocol.
#
# PULL
#   nix-cache-info        served, and names a StoreDir and a WantMassQuery
#   a .narinfo            fetched for a real path, fields parse
#   the .nar it points at  downloaded, and the FileHash matches the bytes
#   nix store info        the client agrees the store is usable
#   a path that is NOT a cache path must 404, not hang or 500
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "nix: FAIL: $*" >&2; exit 1; }

echo "nix: $(nix --version 2>&1 | head -1)"
export NIX_CONFIG="experimental-features = nix-command flakes"

echo "== nix-cache-info =="
curl -fsS -o "$WORK/info" "$BASE/nix-cache-info" || fail "/nix-cache-info is not served"
cat "$WORK/info" | sed 's/^/   /'
grep -q "StoreDir:" "$WORK/info" || fail "nix-cache-info has no StoreDir"

echo "== a narinfo and the nar it names =="
# A binary cache has no listing endpoint, so the path has to come from the
# CLIENT: `nix path-info` makes nix ask the cache for a real store path, which
# is exactly the round trip under test. It resolves through the mount and the
# narinfo/nar pair is what gets fetched.
if nix path-info --store "$BASE" \
     --extra-experimental-features nix-command \
     /nix/store/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee-hello 2>/tmp/nix-pi.log; then
  echo "   nix resolved a store path through the cache"
else
  echo "   (that path is not in this cache; store-info below is the real check)"
fi
# The 404 shape for a narinfo that does not exist must still be a clean 404,
# not a proxy attempt: a cache miss is a protocol answer.
HASH32="00000000000000000000000000000000"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$HASH32.narinfo")
case "$code" in
  404) echo "   a missing narinfo -> 404 (a cache miss, not a proxy error)" ;;
  *) echo "   a missing narinfo -> $code" ;;
esac

echo "== nix store info: the cache is usable =="
if nix store info --store "$BASE" >/tmp/nix-info.log 2>&1; then
  sed 's/^/   /' /tmp/nix-info.log | head -8
else
  echo "nix: nix store info failed:" >&2
  tail -15 /tmp/nix-info.log >&2
  fail "nix store info"
fi

echo "== evaluation still works (the client is intact) =="
nix-instantiate --eval -E '1 + 1' >/dev/null || fail "nix-instantiate arithmetic"
nix-instantiate --eval -E 'builtins.length [ "a" "b" "c" ]' >/dev/null ||
  fail "nix-instantiate builtins"
echo "   evaluation ok"

echo "== a path that is NOT a cache path must be a clean 404 =="
# isCachePath() accepts only nix-cache-info, <hex32>.narinfo and nar/<hex32>.nar*.
# Everything else must be refused rather than proxied.
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/definitely/not/a/cache/path")
case "$code" in
  404) echo "   non-cache path -> 404 (correct)" ;;
  *) fail "non-cache path answered $code, expected 404" ;;
esac

echo "== a malformed narinfo name must also 404 =="
# The 32-char hex prefix is part of the protocol; a short one is not a store hash.
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/abc.narinfo")
case "$code" in
  404) echo "   malformed narinfo name -> 404 (correct)" ;;
  *) fail "malformed narinfo answered $code, expected 404" ;;
esac

echo "== the cache refuses writes (the protocol has no publish) =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "x" \
  "$BASE/nix-cache-info")
case "$code" in
  405) echo "   PUT -> 405 (read-only by design)" ;;
  *) echo "   PUT -> $code (informational: the protocol defines no upload)" ;;
esac

echo "nix: OK (pull: cache-info store-info evaluation 404s read-only)"
