#!/bin/sh
# Conan: point the remote at the artifact mount (ConanCenter layout).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export CONAN_HOME=/tmp/conanhome
mkdir -p "$CONAN_HOME"
conan profile detect --force >/dev/null 2>&1
conan remote add easy-platform "$EASYBLOBS_BASE" --force >/dev/null 2>&1 || true
conan download zlib/1.3.1 -r easy-platform --only-recipe
# Assertion: the pulled recipe is valid and inspectable (conan's own parser).
conan inspect zlib/1.3.1 | head -5
