#!/bin/sh
# Stackage (stackage.org): snapshot config from the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/lts.yaml "$EASYBLOBS_BASE/lts/cabal.config"
grep -qi 'constraints' /tmp/lts.yaml || { echo "stackage: bad cabal.config"; exit 1; }
echo "stackage: lts cabal.config ok"
