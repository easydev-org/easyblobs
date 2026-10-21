#!/bin/sh
# LuaRocks: a plain HTTP tree. Point luarocks at the artifact mount (manifest
# and rock both come from the mirror).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

# Do NOT swallow luarocks' output. It was redirected to /dev/null, so a failure
# said only "luafilesystem not installed" and nothing about why. The rock is
# built from source, so it also needs a C toolchain - make that visible.
echo "luarocks install (server=$EASYBLOBS_BASE)"
if ! luarocks --lua-version=5.4 \
      --server="$EASYBLOBS_BASE" \
      install luafilesystem --tree=/tmp/lr-tree 2>&1 | tail -25; then
  echo "luarocks: install failed (see output above)"
fi
[ -n "$(find /tmp/lr-tree -name 'luafilesystem*.so' 2>/dev/null)" ] || \
  [ -n "$(find /tmp/lr-tree -iname '*luafilesystem*' 2>/dev/null)" ] || {
    echo "luarocks: luafilesystem not installed"; exit 1; }
echo "luarocks: luafilesystem installed"
