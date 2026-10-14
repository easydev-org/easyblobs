#!/bin/sh
# Conda: add the artifact mount as a channel (first-segment = channel key).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# miniconda refuses the public defaults channels unless their ToS is accepted;
# here the channel is the local mirror.
conda tos accept --override-channels --channel "$EASYBLOBS_BASE/pkgs/main" >/dev/null 2>&1 || true
conda create -y -n t --override-channels -c "$EASYBLOBS_BASE/pkgs/main" python zlib
# Run assertion: execute python from the created env and import the package.
conda run -n t python -c 'import zlib; print("conda + zlib:", zlib.ZLIB_VERSION)'
