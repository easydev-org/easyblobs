#!/bin/sh
# LuaRocks: a plain HTTP tree. Point luarocks at the artifact mount (manifest
# and rock both come from the mirror).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

# Do NOT swallow luarocks' output. It was redirected to /dev/null, so a failure
# said only "luafilesystem not installed" and nothing about why. The rock is
# built from source, so it also needs a C toolchain - make that visible.
echo "luarocks install (server=$EASYBLOBS_BASE)"
# The server log showed ZERO requests even with the config in place, so verify
# the mount answers from inside this container BEFORE blaming luarocks - an
# alpine/busybox client resolving differently is a real possibility.
echo "luarocks: probing the mount first"
if command -v wget >/dev/null; then
  for f in manifest manifest-5.4; do
    echo "  probe $f:"
    wget -qS -O /dev/null --timeout=20 "$EASYBLOBS_BASE/$f" 2>&1 | grep -E 'HTTP/|Length|Content-Type' | head -3 || \
      echo "  (no answer for $f)"
  done
fi
# luarocks' own HTTP client is missing on this alpine build: it reported
# "Failed downloading <mount>/manifest-5.4" while `wget` from the SAME container
# got 200 for that exact URL, and the server log shows ZERO requests. luarocks
# needs luasocket/lua-sec for http(s), which are not present.
#
# So the manifest and the rock are fetched with wget (which works) and luarocks
# is pointed at a LOCAL directory. That still exercises the mirror for both
# files - they come from the mount - and it uses luarocks for what it is good
# at: resolving and building from the manifest.
mkdir -p $HOME/rocktree/mirror
echo "luarocks: fetching the manifest from the mount with wget"
wget -q -O $HOME/rocktree/mirror/manifest-5.4 "$EASYBLOBS_BASE/manifest-5.4"
[ -s $HOME/rocktree/mirror/manifest-5.4 ] || { echo "luarocks: empty manifest from the mount"; exit 1; }
echo "luarocks: manifest $(wc -c < $HOME/rocktree/mirror/manifest-5.4) bytes from $EASYBLOBS_BASE"

# The rocks themselves live under the same mount; rewrite manifest entries that
# point at the public site so the download also goes through the mirror.
sed -i "s|https://luarocks.org|$EASYBLOBS_BASE|g; s|http://luarocks.org|$EASYBLOBS_BASE|g" \
  $HOME/rocktree/mirror/manifest-5.4

# Fetch the ROCK ITSELF through the mount as well. luarocks looks for it next to
# the manifest (rocks_servers = a local dir), and reported
#   Could not fetch rock file: File not found: $HOME/rocktree/mirror/luafilesystem-1.9.0-1.src.rock
# The manifest names the file, so read the name from it rather than hard-coding.
ROCK=$(grep -o 'luafilesystem-[0-9.]*-1\.src\.rock' $HOME/rocktree/mirror/manifest-5.4 | head -1)
[ -n "$ROCK" ] || ROCK=luafilesystem-1.9.0-1.src.rock
echo "luarocks: fetching $ROCK from the mount"
if wget -q -O "$HOME/rocktree/mirror/$ROCK" "$EASYBLOBS_BASE/$ROCK"; then
  echo "luarocks: $ROCK $(wc -c < $HOME/rocktree/mirror/$ROCK) bytes from the mount"
else
  echo "luarocks: the mount does not serve $ROCK"
fi

# luarocks looks for `manifest-<luaver>` in the server directory and, for a
# `rocks_servers = { "/dir" }` entry, ALSO rewrites the manifest's download URLs
# against that same directory. The manifest it fetched from luarocks.org uses
# absolute https://luarocks.org URLs, which would bypass the mount - so they are
# rewritten (done above) AND the file is also published under the plain name
# `manifest`, which luarocks falls back to when the versioned one is missing.
cp "$HOME/rocktree/mirror/manifest-5.4" "$HOME/rocktree/mirror/manifest" 2>/dev/null || true
echo "luarocks: mirror directory:"; ls -la "$HOME/rocktree/mirror" | head -6

# NOTE: leafo/gh-actions-luarocks installs its own config into the WORKSPACE's
# .luarocks directory, and that copy wins over $HOME. ci-run.sh runs suites from
# a temp dir, so "$(pwd)" is not the workspace - use GITHUB_WORKSPACE when it
# exists, and fall back to $HOME otherwise.
LRCONF_DIR="${GITHUB_WORKSPACE:-$HOME}/.luarocks"
mkdir -p "$LRCONF_DIR"
# DOUBLE quotes: this is a Lua string that must contain the RESOLVED path. With
# single quotes the shell wrote the literal "$HOME/rocktree/mirror", so the
# config pointed at a directory that does not exist and luarocks found nothing:
#   Error: No results matching query were found for Lua 5.4.
printf '%s\n' \
  "rocks_servers = { \"$HOME/rocktree/mirror\" }" \
  'variables = { LUA_INCDIR = "/usr/include/lua5.4" }' \
  > "$LRCONF_DIR/config-5.4.lua"
export LUAROCKS_CONFIG="$LRCONF_DIR/config-5.4.lua"
echo "luarocks config:"
cat "$LRCONF_DIR/config-5.4.lua"
if ! luarocks --lua-version=5.4 \
      install luafilesystem --tree=/tmp/lr-tree 2>&1 | tail -25; then
  echo "luarocks: install failed (see output above)"
fi
[ -n "$(find /tmp/lr-tree -name 'luafilesystem*.so' 2>/dev/null)" ] || \
  [ -n "$(find /tmp/lr-tree -iname '*luafilesystem*' 2>/dev/null)" ] || {
    echo "luarocks: luafilesystem not installed"; exit 1; }
echo "luarocks: luafilesystem installed"
