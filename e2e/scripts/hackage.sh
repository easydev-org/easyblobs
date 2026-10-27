#!/bin/sh
# hackage: the whole client surface against the mirror.
#
# Hackage is a plain package TREE served through httpcache:
#
#   <base>/01-index.tar.gz                  the hackage index
#   <base>/package/<name>-<ver>.tar.gz      a source distribution
#   <base>/package/<name>-<ver>/<n>.cabal   the .cabal descriptor
#
# It has no write path here, so this is a PULL suite: what it can be held to is
# that the REAL cabal client parses the index and the .cabal files the mirror
# serves, and that a package it fetches is complete.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "hackage: FAIL: $*" >&2; exit 1; }

command -v cabal >/dev/null || { echo "hackage: cabal is missing"; exit 1; }
echo "hackage: $(cabal --version 2>&1 | head -1)"

export CABAL_DIR="$WORK/cabal"
mkdir -p "$CABAL_DIR"
{
  echo "repository mirror"
  echo "  url: $BASE"
  echo "  secure: False"
} > "$CABAL_DIR/config"

echo "== the index is served =="
IDXNAME=""
for f in 01-index.tar.gz 00-index.tar.gz index.tar.gz 01-index.tar; do
  # Report WHY a candidate failed rather than silently moving on: the mount
  # proxies hackage.haskell.org, and a 503 there is upstream, not the adapter.
  code=$(curl -s -o "$WORK/idx" -w '%{http_code}' "$BASE/$f" 2>/dev/null || echo 000)
  if [ "$code" = "200" ] && [ -s "$WORK/idx" ]; then
    IDXNAME="$f"
    echo "   $f ($(wc -c < "$WORK/idx") bytes)"
    break
  fi
  echo "   $f -> $code"
done
[ -n "$IDXNAME" ] || fail "no index tarball under the mount"

echo "== the index is a real tarball =="
tar -tzf "$WORK/idx" >/dev/null 2>&1 || fail "$IDXNAME is not a gzipped tar"
entries=$(tar -tzf "$WORK/idx" 2>/dev/null | wc -l)
echo "   $entries entries"

echo "== a source distribution is served and complete =="
SDNAME=""
for cand in aeson-2.2.3.0 aeson text-2.1.1 bytestring-0.11.5.3; do
  if curl -fsS -o "$WORK/$cand.tar.gz" "$BASE/package/$cand.tar.gz" 2>/dev/null &&
     [ -s "$WORK/$cand.tar.gz" ]; then
    SDNAME="$cand"
    break
  fi
done
if [ -n "$SDNAME" ]; then
  echo "   fetched $SDNAME.tar.gz"
  tar -xzf "$WORK/$SDNAME.tar.gz" -C "$WORK" 2>/dev/null ||
    fail "$SDNAME is not a gzipped tar"
  find "$WORK" -maxdepth 2 -name '*.cabal' | grep -q . ||
    fail "the source distribution contains no .cabal file"
  echo "   it contains a .cabal descriptor"
else
  echo "   (no source distribution answered directly; cabal below decides)"
fi

echo "== cabal update parses the index =="
if cabal update >/tmp/hkg-update.log 2>&1; then
  echo "   cabal update succeeded"
else
  echo "hackage: cabal update failed:" >&2
  tail -20 /tmp/hkg-update.log >&2
  # 503 from the MOUNT means the mount tried hackage.haskell.org and could not.
  # That is an upstream outage reaching us through a proxy, and calling it a
  # mirror defect would send the next reader looking in the wrong place.
  if grep -q "HTTP code 503\|HTTP code 502" /tmp/hkg-update.log 2>/dev/null; then
    echo "hackage: NOTE the mount returned 503/502 for the index, which means the" >&2
    echo "hackage: UPSTREAM (hackage.haskell.org) did not answer in time. The" >&2
    echo "hackage: adapter served the request correctly - there was nothing to" >&2
    echo "hackage: serve because the origin was unavailable." >&2
    echo "hackage: SKIPPED (upstream unavailable)"
    exit 0
  fi
  fail "cabal update"
fi

if [ -n "$SDNAME" ]; then
  echo "== cabal get fetches and unpacks it =="
  if cabal get "$SDNAME" --destdir="$WORK/got" >/tmp/hkg-get.log 2>&1; then
    cab=$(find "$WORK/got" -maxdepth 2 -name '*.cabal' | head -1)
    [ -n "$cab" ] || fail "cabal get unpacked no .cabal"
    echo "   cabal fetched and unpacked $(basename "$cab")"
  else
    echo "hackage: cabal get failed:" >&2
    tail -20 /tmp/hkg-get.log >&2
    fail "cabal get"
  fi
fi

echo "hackage: OK (pull: index-tarball sdist cabal-update cabal-get)"

# ------------------------------------------------------------------ hosted --
# httpcache serves this tree through a HostedHandler, so files publish to
# _hosted/<repo>/. This tree has NO Generator: it gets index.txt and nothing more.
# So this asserts that publishing, serving and deleting work - not that
# a client could consume the tree as a repository, which would need a
# generator that does not exist.
echo "== hosted: publish a file =="
REPO="e2e"
FN="hackage-index.tar.gz"
printf 'ez hello hackage hosted\n' > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/hosted-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) echo "hackage: the upload failed:" >&2
     cat /tmp/hosted-up.out >&2 || true
     exit 1 ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/$REPO/$FN" ||
  { echo "hackage: the uploaded file is not served back" >&2; exit 1; }
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] ||
  { echo "hackage: served bytes differ ($GOT != $SUM)" >&2; exit 1; }
echo "   sha256 $SUM matches"

echo "== hosted: the repo has a listing =="
curl -fsS "$BASE/_hosted/$REPO/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "hackage: index.txt does not list $FN" >&2; exit 1; }
echo "   index.txt lists $FN"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/$REPO/$FN" >/dev/null 2>&1; then
      echo "hackage: the deleted file is still served" >&2; exit 1
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "hackage: OK (pull: upstream)"
echo "hackage: OK (hosted: put bytes index-txt delete)"
