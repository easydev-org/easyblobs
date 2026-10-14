#!/bin/sh
# JSR npm-compatibility registry (npm.jsr.io): deno/bun/npm resolve @jsr/* here
# as ordinary npm packuments. Fetch the packument from the named target mount,
# then follow the dist.tarball URL it advertises (the adapter rewrites it to
# itself, so this exercises the packument-tarball fallback).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/packument.json "$EASYBLOBS_BASE/@jsr%2Fluca__flag"
grep -q '"name":"@jsr/luca__flag"' /tmp/packument.json || { echo "jsrnpm: bad packument"; exit 1; }
tarball=$(sed -n 's/.*"tarball":"\([^"]*\)".*/\1/p' /tmp/packument.json | head -1)
[ -n "$tarball" ] || { echo "jsrnpm: no tarball in packument"; exit 1; }
wget -qO /tmp/pkg.tgz "$tarball"
tar -tzf /tmp/pkg.tgz >/dev/null || { echo "jsrnpm: bad tarball"; exit 1; }
echo "jsrnpm: packument + tarball ok ($tarball)"
