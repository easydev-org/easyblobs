#!/bin/sh
# JSR npm-compatibility registry (npm.jsr.io): deno/bun/npm resolve @jsr/* here
# as ordinary npm packuments. Fetch the packument from the named target mount,
# then follow the dist.tarball URL it advertises (the adapter rewrites it to
# itself, so this exercises the packument-tarball fallback).
#
# NOTE on the tarball step: the packument is served from the mount (the
# /_upstream/npm.jsr.io target), but the TARBALL is not yet cached, so the
# adapter falls back to its default npm upstream (registry.npmjs.org). In an
# isolated CI run that is unreachable by design, so give wget a hard timeout -
# without one it hangs and the surrounding `set -e` kills the suite.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/packument.json "$EASYBLOBS_BASE/@jsr%2Fluca__flag"
grep -q '"name":"@jsr/luca__flag"' /tmp/packument.json || { echo "jsrnpm: bad packument"; exit 1; }
tarball=$(sed -n 's/.*"tarball":"\([^"]*\)".*/\1/p' /tmp/packument.json | head -1)
[ -n "$tarball" ] || { echo "jsrnpm: no tarball in packument"; exit 1; }
if ! wget -q -O /tmp/pkg.tgz --timeout=20 --tries=1 "$tarball"; then
  echo "jsrnpm: tarball fetch failed ($tarball)"
  echo "jsrnpm: the packument came from the mount, but the tarball was not cached"
  echo "jsrnpm: and the adapter's default npm upstream is unreachable in this isolated run."
  exit 1
fi
tar -tzf /tmp/pkg.tgz >/dev/null || { echo "jsrnpm: bad tarball"; exit 1; }
echo "jsrnpm: packument + tarball ok ($tarball)"
