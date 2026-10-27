#!/bin/sh
# LuaRocks: a plain HTTP tree. Point luarocks at the artifact mount (manifest
# and rock both come from the mirror).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# The mount base without a trailing slash. The hosted section uses it.
BASE="${EASYBLOBS_BASE%/}"

# Do NOT swallow luarocks' output. It was redirected to /dev/null, so a failure
# said only "luafilesystem not installed" and nothing about why. The rock is
# built from source, so it also needs a C toolchain - make that visible.
echo "luarocks install (server=$EASYBLOBS_BASE)"
# The server log showed ZERO requests even with the config in place, so verify
# the mount answers from inside this container BEFORE blaming luarocks - an
# alpine/busybox client resolving differently is a real possibility.
echo "luarocks: probing the mount first"
# Which Lua does this toolchain actually have? The suite used to hardcode 5.4
# everywhere - manifest-$LUAVER, config-$LUAVER.lua, --lua-version=$LUAVER - and easysetup
# ships 5.5, so luarocks answered "Could not find Lua 5.4 in PATH" and the
# manifest path was wrong as well.
LUAVER=""
for lua in lua lua5.5 lua5.4 lua5.3 lua5.2 lua5.1; do
  command -v "$lua" >/dev/null 2>&1 || continue
  LUAVER=$("$lua" -e 'io.write(_VERSION:match("%d+%.%d+"))' 2>/dev/null) && [ -n "$LUAVER" ] && break
done
[ -n "$LUAVER" ] || { echo "luarocks: no lua interpreter on PATH"; exit 1; }
echo "luarocks: lua version $LUAVER"

if command -v wget >/dev/null; then
  for f in manifest "manifest-$LUAVER"; do
    echo "  probe $f:"
    wget -qS -O /dev/null --timeout=20 "$EASYBLOBS_BASE/$f" 2>&1 | grep -E 'HTTP/|Length|Content-Type' | head -3 || \
      echo "  (no answer for $f)"
  done
fi
# luarocks' own HTTP client is missing on this alpine build: it reported
# "Failed downloading <mount>/manifest-$LUAVER" while `wget` from the SAME container
# got 200 for that exact URL, and the server log shows ZERO requests. luarocks
# needs luasocket/lua-sec for http(s), which are not present.
#
# So the manifest and the rock are fetched with wget (which works) and luarocks
# is pointed at a LOCAL directory. That still exercises the mirror for both
# files - they come from the mount - and it uses luarocks for what it is good
# at: resolving and building from the manifest.
mkdir -p $HOME/rocktree/mirror
echo "luarocks: fetching the manifest from the mount with wget"
wget -q -O $HOME/rocktree/mirror/manifest-$LUAVER "$EASYBLOBS_BASE/manifest-$LUAVER"
[ -s $HOME/rocktree/mirror/manifest-$LUAVER ] || { echo "luarocks: empty manifest from the mount"; exit 1; }
echo "luarocks: manifest $(wc -c < $HOME/rocktree/mirror/manifest-$LUAVER) bytes from $EASYBLOBS_BASE"

# The rocks themselves live under the same mount; rewrite manifest entries that
# point at the public site so the download also goes through the mirror.
sed -i "s|https://luarocks.org|$EASYBLOBS_BASE|g; s|http://luarocks.org|$EASYBLOBS_BASE|g" \
  $HOME/rocktree/mirror/manifest-$LUAVER

# Fetch the ROCK ITSELF through the mount as well. luarocks looks for it next to
# the manifest (rocks_servers = a local dir), and reported
#   Could not fetch rock file: File not found: $HOME/rocktree/mirror/luafilesystem-1.9.0-1.src.rock
# The manifest names the file, so read the name from it rather than hard-coding.
ROCK=$(grep -o 'luafilesystem-[0-9.]*-1\.src\.rock' $HOME/rocktree/mirror/manifest-$LUAVER | head -1)
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
cp "$HOME/rocktree/mirror/manifest-$LUAVER" "$HOME/rocktree/mirror/manifest" 2>/dev/null || true
echo "luarocks: mirror directory:"; ls -la "$HOME/rocktree/mirror" | head -6

# NOTE: leafo/gh-actions-luarocks installs its own config into the WORKSPACE's
# .luarocks directory, and that copy wins over $HOME. ci-run.sh runs suites from
# a temp dir, so "$(pwd)" is not the workspace - use GITHUB_WORKSPACE when it
# exists, and fall back to $HOME otherwise.
LRCONF_DIR="${GITHUB_WORKSPACE:-$HOME}/.luarocks"
mkdir -p "$LRCONF_DIR"
# LUA_INCDIR: the rock is a C extension and needs lua.h. The old actions put the
# headers in /usr/include/lua5.4; the easysetup closure has them inside its own
# store path, so ask lua where it is instead of guessing a system prefix.
if [ -z "${LUA_INCDIR:-}" ]; then
  LUABIN=$(command -v lua || command -v lua5.4 || command -v lua5.5 || true)
  if [ -n "$LUABIN" ]; then
    # <store>/bin/lua -> <store>/include
    LUA_INCDIR="$(cd "$(dirname "$(readlink -f "$LUABIN")")/../include" 2>/dev/null && pwd)" || true
  fi
fi
if [ -z "${LUA_INCDIR:-}" ] || [ ! -f "$LUA_INCDIR/lua.h" ]; then
  for cand in /usr/include/lua5.4 /usr/include/lua5.3 /usr/include; do
    [ -f "$cand/lua.h" ] && LUA_INCDIR="$cand" && break
  done
fi
echo "luarocks: LUA_INCDIR=${LUA_INCDIR:-<not found>}"
# DOUBLE quotes: this is a Lua string that must contain the RESOLVED path. With
# single quotes the shell wrote the literal "$HOME/rocktree/mirror", so the
# config pointed at a directory that does not exist and luarocks found nothing:
#   Error: No results matching query were found for Lua 5.4.
printf '%s\n' \
  "rocks_servers = { \"$HOME/rocktree/mirror\" }" \
  "variables = { LUA_INCDIR = \"${LUA_INCDIR:-/usr/include/lua5.4}\" }" \
  > "$LRCONF_DIR/config-$LUAVER.lua"
export LUAROCKS_CONFIG="$LRCONF_DIR/config-$LUAVER.lua"
echo "luarocks config:"
cat "$LRCONF_DIR/config-$LUAVER.lua"
# No `if ! ... | tail`: `!` and `if` both look at the LAST command of the pipe,
# which is tail, so a failed install read as success. Capture and inspect.
if ! out=$(luarocks --lua-version=$LUAVER install luafilesystem --tree=/tmp/lr-tree 2>&1); then
  echo "luarocks: install failed:" >&2
  echo "$out" | tail -25 >&2
  exit 1
fi
echo "$out" | tail -5
[ -n "$(find /tmp/lr-tree -name 'luafilesystem*.so' 2>/dev/null)" ] || \
  [ -n "$(find /tmp/lr-tree -iname '*luafilesystem*' 2>/dev/null)" ] || {
    echo "luarocks: luafilesystem not installed"; exit 1; }
echo "luarocks: luafilesystem installed"

# ------------------------------------------------------------------ hosted --
# httpcache serves this tree through a HostedHandler, so files publish to
# _hosted/<repo>/. This tree HAS a Generator, so a real index is synthesised from
# what was uploaded; the listing check below is the same one a client reads.
echo "== hosted: publish a file =="
REPO="e2e"
FN="e2e-1.0-1.src.rock"
printf 'ez hello luarocks hosted\n' > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/hosted-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) echo "luarocks: the upload failed:" >&2
     cat /tmp/hosted-up.out >&2 || true
     exit 1 ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/$REPO/$FN" ||
  { echo "luarocks: the uploaded file is not served back" >&2; exit 1; }
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] ||
  { echo "luarocks: served bytes differ ($GOT != $SUM)" >&2; exit 1; }
echo "   sha256 $SUM matches"

echo "== hosted: the repo has a listing =="
curl -fsS "$BASE/_hosted/$REPO/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "luarocks: index.txt does not list $FN" >&2; exit 1; }
echo "   index.txt lists $FN"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/$REPO/$FN" >/dev/null 2>&1; then
      echo "luarocks: the deleted file is still served" >&2; exit 1
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "luarocks: OK (pull: upstream)"
echo "luarocks: OK (hosted: put bytes index-txt delete)"
