#!/bin/sh
# JSR native registry API (jsr.io): package metadata + a module file, served
# from the artifact mount (plain GETs on a path tree).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/meta.json "$EASYBLOBS_BASE/@luca/flag/meta.json"
grep -q '"scope": *"luca"' /tmp/meta.json || { echo "jsr: bad meta"; exit 1; }
wget -qO /tmp/vmeta.json "$EASYBLOBS_BASE/@luca/flag/1.0.1_meta.json"
grep -q 'manifest' /tmp/vmeta.json || { echo "jsr: bad version meta"; exit 1; }
wget -qO /tmp/main.ts "$EASYBLOBS_BASE/@luca/flag/1.0.1/main.ts"
grep -qi 'export' /tmp/main.ts || { echo "jsr: bad module"; exit 1; }
echo "jsr: meta + version meta + module ok"
