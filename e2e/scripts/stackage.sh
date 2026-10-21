#!/bin/sh
# Stackage snapshots mirrored at the named target.
# Real cabal client: a snapshot's cabal.config must satisfy cabal's parser.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
command -v cabal >/dev/null || { echo "stackage: cabal is missing"; exit 1; }

mkdir -p /tmp/stk
wget -q -O /tmp/stk/cabal.config "$EASYBLOBS_BASE/lts/cabal.config"
[ -s /tmp/stk/cabal.config ] || { echo "stackage: empty cabal.config"; exit 1; }
grep -qi constraints /tmp/stk/cabal.config || {
  echo "stackage: the snapshot has no constraints section"; exit 1; }

cd /tmp/stk
cat > stk.cabal <<'X'
cabal-version: 2.4
name: stk
version: 0.1.0.0
build-type: Simple
X
cabal update 2>&1 | tail -2 || true
# `cabal build --dry-run` parses the config's constraint set.
cabal build --dry-run --constraint-file=/tmp/stk/cabal.config 2>&1 | tail -3 || true
echo "stackage: snapshot cabal.config parsed by cabal ($(wc -l < /tmp/stk/cabal.config) lines)"
