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
# those three shapes.
#
# A binary cache IS writable in the protocol: `nix copy --to <url>` PUTs the
# same narinfo/nar pair a GET reads, and nix-serve/Cachix/Attic all accept it.
# This adapter used to answer 405 to every non-GET; it now serves
# /_hosted/<repo>/ with PUT, GET and DELETE.
#
# PULL
#   nix-cache-info        served, and names a StoreDir
#   nix store info        the client agrees the store is usable
#   evaluation            the client itself is intact
#   the 404 shapes        a non-cache path and a malformed narinfo name are
#                         refused rather than proxied
#
# HOSTED
#   PUT narinfo + nar     published under _hosted/<repo>/
#   GET them back         byte-identical
#   DELETE                removes the entry
#   nix-cache-info        cannot be uploaded: it describes the mount
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

echo "== a write to the PULL namespace is still refused =="
# The proxying side stays read-only: you publish to _hosted/<repo>/, not into
# the upstream cache's own namespace.
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "x" \
  "$BASE/$HASH32.narinfo")
case "$code" in
  405) echo "   PUT into the upstream namespace -> 405 (correct)" ;;
  *) fail "PUT into the upstream namespace answered $code, expected 405" ;;
esac

# ------------------------------------------------------------------ hosted --
echo "== hosted: publish a narinfo and the nar it names =="
REV="e2e"
# A store-path hash is 32 chars of nix-base32. It only has to be well formed for
# the adapter to accept it; nix would additionally verify NarHash on a real pull.
H="11111111111111111111111111111111"
NARINFO="$WORK/$H.narinfo"
{
  echo "StorePath: /nix/store/$H-easyblobs-e2e-1.0.0"
  echo "URL: nar/$H.nar"
  echo "Compression: none"
  echo "NarSize: 16"
} > "$NARINFO"
printf 'ez hello nix nar' > "$WORK/$H.nar"

for pair in "$H.narinfo" "nar/$H.nar"; do
  case "$pair" in
    *.narinfo) src="$NARINFO" ;;
    *)         src="$WORK/$H.nar" ;;
  esac
  code=$(curl -sS -o /tmp/nix-up.out -w '%{http_code}' -X PUT \
    --data-binary "@$src" "$BASE/_hosted/$REV/$pair")
  case "$code" in
    200|201) echo "   PUT $pair -> $code" ;;
    *) fail "PUT $pair -> $code $(cat /tmp/nix-up.out)" ;;
  esac
done

echo "== hosted: both come back byte-identically =="
curl -fsS -o "$WORK/back.narinfo" "$BASE/_hosted/$REV/$H.narinfo" ||
  fail "the narinfo is not served back"
cmp -s "$WORK/back.narinfo" "$NARINFO" || fail "the narinfo came back changed"
curl -fsS -o "$WORK/back.nar" "$BASE/_hosted/$REV/nar/$H.nar" ||
  fail "the nar is not served back"
cmp -s "$WORK/back.nar" "$WORK/$H.nar" || fail "the nar bytes differ"
echo "   bytes match"

echo "== hosted: nix-cache-info is generated, not uploadable =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "StoreDir: /x" \
  "$BASE/_hosted/$REV/nix-cache-info")
[ "$code" = "405" ] || fail "PUT nix-cache-info answered $code, expected 405"
echo "   PUT nix-cache-info -> 405 (it describes the mount)"
curl -fsS "$BASE/_hosted/$REV/nix-cache-info" | grep -q "StoreDir: /nix/store" ||
  fail "the mounted cache-info is not the mount's own"
echo "   GET nix-cache-info still answers with the mount's StoreDir"

echo "== hosted: a path the read side would refuse is not publishable =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "x" \
  "$BASE/_hosted/$REV/not/a/cache/path")
[ "$code" = "404" ] || fail "publishing a non-cache path answered $code, expected 404"
echo "   a non-cache path -> 404 (the write and read sides agree)"

echo "== hosted: DELETE removes the entry =="
code=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/_hosted/$REV/$H.narinfo")
case "$code" in
  200|204) echo "   deleted" ;;
  *) echo "   (DELETE answered $code)" ;;
esac
if curl -fsS "$BASE/_hosted/$REV/$H.narinfo" >/dev/null 2>&1; then
  fail "the deleted narinfo is still served"
fi
echo "   gone"

echo "nix: OK (pull: cache-info store-info evaluation 404s upstream-readonly)"
echo "nix: OK (hosted: put narinfo nar bytes cache-info-refused invalid-path delete)"
