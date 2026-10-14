#!/bin/sh
# Bazel Central Registry (bcr.bazel.build): module metadata + source archive ref,
# served from the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/bcr.json "$EASYBLOBS_BASE/modules/rules_go/metadata.json"
grep -q 'rules_go' /tmp/bcr.json || { echo "bazel: bad metadata"; exit 1; }
wget -qO /tmp/bcr-src.json "$EASYBLOBS_BASE/modules/rules_go/0.50.1/source.json"
grep -q 'url' /tmp/bcr-src.json || { echo "bazel: bad source.json"; exit 1; }
echo "bazel: metadata + source.json ok"
