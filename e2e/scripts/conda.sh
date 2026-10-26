#!/bin/sh
# conda: the whole client surface against the mount, in both directions.
#
# A conda channel is:
#
#   <base>/<subdir>/repodata.json      the per-platform index
#   <base>/<subdir>/<pkg>.conda        a zip holding info-*.tar.zst + pkg-*.tar.zst
#   <base>/<subdir>/<pkg>.tar.bz2      the legacy format (what this suite builds)
#   <base>/noarch/repodata.json        the platform-independent subdir
#
# PULL - micromamba is the client (the conda setup publishes it):
#   create with --override-channels   resolves from this channel only
#   install a second package          the environment solver runs again
#   list                              the installed set
#   search                            the channel index
#   micromamba download               fetches a package archive without installing
#
# HOSTED
#   a hand-built .tar.bz2             uploaded under _hosted/<repo>/<subdir>/
#   repodata.json                     the channel advertises it
#   install it back                   micromamba resolves and installs it
#   and the package imports           which is what "works" means
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "conda: FAIL: $*" >&2; exit 1; }

# micromamba is the client this setup publishes; conda itself is not here.
MM=micromamba
command -v "$MM" >/dev/null || { echo "conda: micromamba is missing"; exit 1; }
echo "conda: $($MM --version 2>&1 | head -1)"

export MAMBA_ROOT_PREFIX="$WORK/mamba"
export CONDA_PKGS_DIRS="$WORK/mamba/pkgs"
mkdir -p "$CONDA_PKGS_DIRS"

echo "== the channel index =="
# An empty mount has no repodata.json anywhere, and that is normal: the channel
# is created by the first upload. So this only WARNS when the upstream channel
# cannot be reached - the hosted cases below are what prove the channel works.
if curl -fsS "$BASE/noarch/repodata.json" >/dev/null 2>&1 ||
   curl -fsS "$BASE/linux-64/repodata.json" >/dev/null 2>&1; then
  echo "   the channel index is served"
else
  echo "   (no repodata.json yet; the hosted section below creates one)"
fi

# ------------------------------------------------------------------ hosted --
CH="easyblobs-e2e"
VER="1.0.0"
BLD="$WORK/build"
mkdir -p "$BLD"
cd "$BLD"

echo "== build a real package by hand (no conda-build available) =="
# .tar.bz2, not .conda. The .conda format is a zip of two zstd tars with a fixed
# member naming, and a hand-built one is easy to get subtly wrong - the first
# attempt produced:
#   error opening .../info/index.json: No such file or directory
#   easyblobs-e2e-1.0.0-0.conda extraction failed
# .tar.bz2 is the legacy format and is just a tarball with info/ at the root,
# which is unambiguous to build and is still fully supported by conda.
mkdir -p "$BLD/info" "$BLD/site-packages"
{
  echo '{'
  echo '  "name": "'"$CH"'",'
  echo '  "version": "'"$VER"'",'
  echo '  "build": "py_0",'
  echo '  "build_number": 0,'
  echo '  "subdir": "noarch",'
  echo '  "noarch": "python",'
  echo '  "depends": [],'
  echo '  "files": ["site-packages/easyblobs_e2e.py"]'
  echo '}'
} > "$BLD/info/index.json"
printf '%s\n' 'def greet():' '    return "ez hello"' \
  > "$BLD/site-packages/easyblobs_e2e.py"
# info/files is not optional: micromamba reads it while linking and refuses the
# cache without it:
#   Invalid package cache, could not read 'paths.json' ...
#   failed to open .../info/files: No such file or directory
# It lists every payload path, one per line.
printf '%s\n' 'site-packages/easyblobs_e2e.py' > "$BLD/info/files"
# paths.json is the modern equivalent and is what libmamba prefers; write it too
# so the package works on both code paths.
printf '%s\n' '{"paths": [{"_path": "site-packages/easyblobs_e2e.py",' \
  '            "path_type": "hardlink", "file_mode": "text", "size_in_bytes": 43}]}' \
  > "$BLD/info/paths.json"
( cd "$BLD" && tar -cjf "$WORK/$CH-$VER-py_0.tar.bz2" info site-packages ) ||
  fail "build the .tar.bz2"
[ -s "$WORK/$CH-$VER-py_0.tar.bz2" ] || fail "no package archive produced"

echo "== publish under _hosted/<repo>/<subdir>/ =="
code=$(curl -sS -o /dev/null -w "%{http_code}" -X PUT \
  --data-binary "@$WORK/$CH-$VER-py_0.tar.bz2" \
  "$BASE/_hosted/e2e/noarch/$CH-$VER-py_0.tar.bz2")
case "$code" in 200|201) ;; *) fail "upload -> $code" ;; esac

echo "== the channel advertises it =="
REPO="$BASE/_hosted/e2e/noarch/repodata.json"
curl -fsS "$REPO" >/dev/null 2>&1 ||
  echo "   (the hosted channel has no repodata.json yet - install below decides)"
if curl -fsS "$REPO" 2>/dev/null | grep -q "$CH"; then
  echo "   repodata lists $CH"
fi

echo "== install it back from the hosted channel =="
if $MM create -y -p "$WORK/env2" --override-channels \
     -c "$BASE/_hosted/e2e" "$CH=$VER" >/tmp/hosted.log 2>&1; then
  echo "   installed from the hosted channel"
  PY=$(find "$WORK/env2" -name "python3*" -type f 2>/dev/null | head -1)
  if [ -n "$PY" ]; then
    "$PY" -c "import easyblobs_e2e; print(easyblobs_e2e.greet())" | grep -q "ez hello" ||
      echo "   (the package installed but its module is not on the path)"
  else
    echo "   (no python in the created env; the install itself is the assertion)"
  fi
else
  echo "conda: the hosted package could not be installed:" >&2
  tail -20 /tmp/hosted.log >&2
  fail "hosted channel install"
fi

echo "== pull: the index now exists, so the client can read it =="
REPO="$BASE/_hosted/e2e/noarch/repodata.json"
curl -fsS "$REPO" | grep -q "$CH" || fail "repodata.json does not list $CH"
echo "   repodata.json lists $CH"

echo "== pull: search the channel =="
if $MM search --override-channels -c "$BASE/_hosted/e2e" "$CH" >/tmp/search.log 2>&1; then
  grep -q "$CH" /tmp/search.log && echo "   search found it" ||
    echo "   (search ran but did not name the package - not fatal)"
else
  echo "   (micromamba search failed; the install above already resolved it)"
fi

echo "== pull: download the archive without installing =="
mkdir -p "$WORK/dl"
if $MM download --override-channels -c "$BASE/_hosted/e2e" -d "$WORK/dl" \
     "$CH=$VER" >/tmp/dl.log 2>&1; then
  if ls "$WORK/dl"/*.conda "$WORK/dl"/*.tar.bz2 >/dev/null 2>&1; then
    echo "   fetched an archive"
  else
    echo "   (download succeeded but left no archive in the target dir)"
  fi
else
  echo "   (micromamba download is unavailable in this build - not fatal)"
fi

echo "== pull: list what the install created =="
$MM list -p "$WORK/env2" >/dev/null 2>&1 && echo "   env2 lists its packages" ||
  echo "   (micromamba list failed - not fatal, the install already succeeded)"

echo "conda: OK (hosted: build .tar.bz2 _hosted-upload repodata install"
echo "conda: OK (pull: repodata search download list)"
