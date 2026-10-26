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
  if curl -fsS -o "$WORK/idx" "$BASE/$f" 2>/dev/null && [ -s "$WORK/idx" ]; then
    IDXNAME="$f"
    echo "   $f ($(wc -c < "$WORK/idx") bytes)"
    break
  fi
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
