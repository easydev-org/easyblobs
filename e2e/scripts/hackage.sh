#!/bin/sh
# Hackage mirrored at the named target: resolve and download a package with
# the REAL cabal client (it must parse the index and the .cabal file).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

command -v cabal >/dev/null || { echo "hackage: cabal is missing"; exit 1; }

export CABAL_DIR=/tmp/cabal
rm -rf "$CABAL_DIR"
mkdir -p "$CABAL_DIR"
cat > "$CABAL_DIR/config" <<CFG
repository mirror
  url: $EASYBLOBS_BASE
  secure: False
CFG

cabal update 2>&1 | tail -3
cabal get aeson-2.2.3.0 --destdir=/tmp/hkg 2>&1 | tail -3

cabal=$(find /tmp/hkg -maxdepth 2 -name '*.cabal' | head -1)
[ -n "$cabal" ] || { echo "hackage: cabal did not unpack aeson"; exit 1; }
echo "hackage: cabal fetched and unpacked $(basename "$cabal")"
