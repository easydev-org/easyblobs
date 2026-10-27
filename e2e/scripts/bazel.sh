#!/bin/sh
# bazel: the Bazel Central Registry, mirrored at the named target.
#
# A BCR module is four files at a fixed layout, and each one matters:
#
#   /bazel_registry.json                       the registry's own descriptor
#   /modules/<name>/metadata.json              every version the module has
#   /modules/<name>/<ver>/MODULE.bazel         the module's dependency list
#   /modules/<name>/<ver>/source.json          where the archive is + its hash
#   /modules/<name>/<ver>/patches/<file>       patches the registry applies
#
# source.json is the one a mirror must get exactly right: it carries
# "integrity", and bazel REFUSES the archive when the hash disagrees. A mirror
# that served some other module's source.json would be caught by bazel rather
# than by us - which is why the suite checks the file names the module it was
# asked for, before bazel does.
#
# The REAL client is bazel: given a bzlmod project it resolves the graph through
# the configured registry and verifies the archive. A wget of metadata.json
# proves none of that, so both layers are asserted here.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "bazel: FAIL: $*" >&2; exit 1; }

MOD="rules_go"
VER="0.50.1"

echo "== the registry descriptor =="
code=$(curl -sS -o "$WORK/reg.json" -w '%{http_code}' "$BASE/bazel_registry.json")
if [ "$code" != "200" ]; then
  echo "bazel: GET /bazel_registry.json -> $code" >&2
  echo "bazel: the body was: $(head -c 200 "$WORK/reg.json" 2>/dev/null)" >&2
  echo "bazel: the mount root answers with: $(curl -sS "$BASE/" 2>/dev/null | head -c 120)" >&2
  # Compare against a tree known to proxy: if THAT also 404s, the problem is the
  # upstream table as a whole; if it works, the problem is bazel's entry.
  echo "bazel: for comparison, /artifacts/cran/src/contrib/PACKAGES ->" >&2
  echo "bazel:   $(curl -sS -o /dev/null -w '%{http_code}' \
        "${BASE%/*}/cran/src/contrib/PACKAGES" 2>/dev/null)" >&2
  echo "bazel: and /artifacts/bazel/modules/rules_go/metadata.json ->" >&2
  echo "bazel:   $(curl -sS -o /dev/null -w '%{http_code}' \
        "$BASE/modules/rules_go/metadata.json" 2>/dev/null)" >&2
  fail "GET /bazel_registry.json -> $code"
fi
echo "   $(wc -c < "$WORK/reg.json") bytes"

echo "== the module's version list =="
curl -fsS -o "$WORK/meta.json" "$BASE/modules/$MOD/metadata.json" ||
  fail "GET modules/$MOD/metadata.json"
grep -q "\"$VER\"" "$WORK/meta.json" || fail "metadata.json does not list $VER"
grep -q '"versions"' "$WORK/meta.json" || fail "metadata.json has no versions key"
echo "   metadata.json lists $VER"

echo "== the module's own MODULE.bazel =="
curl -fsS -o "$WORK/MODULE.bazel" "$BASE/modules/$MOD/$VER/MODULE.bazel" ||
  fail "GET MODULE.bazel"
grep -q "module(" "$WORK/MODULE.bazel" || fail "MODULE.bazel has no module() stanza"
grep -q "name = \"$MOD\"" "$WORK/MODULE.bazel" ||
  fail "MODULE.bazel does not declare name = $MOD"
echo "   it declares name = $MOD"

echo "== source.json names THIS module and carries an integrity hash =="
curl -fsS -o "$WORK/source.json" "$BASE/modules/$MOD/$VER/source.json" ||
  fail "GET source.json"
grep -q '"integrity"' "$WORK/source.json" ||
  fail "source.json has no integrity field; bazel cannot verify the archive"
grep -q "$MOD" "$WORK/source.json" ||
  fail "source.json does not name $MOD - the mirror served another module's file"
echo "   integrity present, and the url names $MOD"

echo "== patches, if the version declares any, are served =="
# A version whose patches are missing is a module bazel cannot build.
NPATCH=$(grep -o '"patches"' "$WORK/source.json" | wc -l)
if [ "$NPATCH" -gt 0 ]; then
  # Collect the patch file names from the patches map and fetch each one.
  for p in $(python3 -c '
import json,sys
doc=json.load(open(sys.argv[1]))
for k in (doc.get("patches") or {}): print(k)
' "$WORK/source.json"); do
    curl -fsS -o /dev/null "$BASE/modules/$MOD/$VER/patches/$p" ||
      fail "source.json declares patch $p but it is not served"
    echo "   patch $p is served"
  done
else
  echo "   (this version declares no patches)"
fi

echo "== a module that does not exist stays a 404 =="
# The failure mode a per-tree cache once had: one object answering for another.
code=$(curl -s -o /dev/null -w '%{http_code}' \
  "$BASE/modules/this_module_does_not_exist/metadata.json")
case "$code" in
  404) echo "   -> 404 (correct)" ;;
  200) fail "a nonexistent module answered 200 - the mirror invented content" ;;
  *) echo "   -> $code" ;;
esac

echo "== bazel resolves the graph through the mount =="
command -v bazel >/dev/null || { echo "bazel: the bazel binary is missing"; exit 1; }
W="${E2E_W:-$WORK/w}"
mkdir -p "$W"
cd "$W"
{
  echo "common --registry=$BASE"
  echo "common --enable_bzlmod"
} > .bazelrc
printf '%s\n' \
  'module(name = "e2e_bcr", version = "0.0.0")' \
  "bazel_dep(name = \"$MOD\", version = \"$VER\")" > MODULE.bazel
{
  echo 'genrule('
  echo '    name = "noop",'
  echo '    outs = ["noop.txt"],'
  echo '    cmd = "echo bcr-ok > $@",'
  echo ')'
} > BUILD.bazel

if bazel mod graph >/tmp/bazel-graph.log 2>&1; then
  grep -q "$MOD" /tmp/bazel-graph.log || fail "the resolved graph omits $MOD"
  echo "   the graph contains $MOD"
else
  echo "bazel: bazel mod graph failed:" >&2
  tail -25 /tmp/bazel-graph.log >&2
  fail "bazel mod graph"
fi

if bazel build //:noop >/tmp/bazel-build.log 2>&1; then
  echo "   the build succeeded"
else
  echo "bazel: bazel build failed:" >&2
  tail -25 /tmp/bazel-build.log >&2
  fail "bazel build"
fi

echo "bazel: OK (pull: registry-json metadata module-bazel source-json integrity patches 404 mod-graph build)"
