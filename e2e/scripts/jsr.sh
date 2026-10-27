#!/bin/sh
# jsr: the whole client surface against the mirror, in both directions.
#
# JSR's native registry protocol. Deno asks for three things, and a hosted repo
# that cannot answer all three is not a repository a client can install from:
#
#   GET /@scope/pkg/meta.json           scope, name, latest, versions{}
#   GET /@scope/pkg/<ver>_meta.json     manifest{path:{size,checksum}},
#                                       moduleGraph1{}, exports{}
#   GET /@scope/pkg/<ver>/<path>        the module's bytes
#
# PULL   the REAL deno client caches a package through the mount, then IMPORTS
#        it and calls it.
# HOSTED a package is uploaded file by file, the registry documents are
#        GENERATED from those bytes, and deno then installs and runs it. The
#        generated meta.json is asserted to carry a real sha256 checksum, because
#        that is the field deno verifies.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "jsr: FAIL: $*" >&2; exit 1; }

command -v deno >/dev/null || { echo "jsr: deno is missing"; exit 1; }
echo "jsr: $(deno --version 2>&1 | head -1)"

export DENO_DIR="$WORK/deno"
mkdir -p "$DENO_DIR"

echo "== pull: the three registry documents =="
curl -fsS -o "$WORK/meta.json" "$BASE/@luca/flag/meta.json" || fail "meta.json"
curl -fsS -o "$WORK/vmeta.json" "$BASE/@luca/flag/1.0.1_meta.json" || fail "_meta.json"
curl -fsS -o "$WORK/main.ts" "$BASE/@luca/flag/1.0.1/main.ts" || fail "the module"
grep -q '"manifest"' "$WORK/vmeta.json" || fail "_meta.json has no manifest"
grep -q "export" "$WORK/main.ts" || fail "the module has no exports"
echo "   meta + version meta + module all parse"

echo "== pull: deno caches the package and RUNS it =="
printf '%s\n' \
  'import { printProgress } from "jsr:@luca/flag@1.0.1";' \
  'console.log("printProgress type:", typeof printProgress);' > "$WORK/pull.ts"
# printProgress is the package's ONLY export - read off the real module.
# Naming one it does not export fails at load time and reads like a registry
# problem:
#   The requested module does not provide an export named 'flag'
if deno run -q "$WORK/pull.ts" >/tmp/jsr-pull.log 2>&1; then
  cat /tmp/jsr-pull.log | sed 's/^/   /'
  grep -q "printProgress type" /tmp/jsr-pull.log || fail "deno ran but printed nothing"
else
  echo "jsr: deno could not run the module through the mirror:" >&2
  tail -20 /tmp/jsr-pull.log >&2
  fail "deno run (pull)"
fi

# ------------------------------------------------------------------ hosted --
# The operator publishes SOURCES. meta.json and <ver>_meta.json are derived from
# those bytes by the adapter's generator - hand-writing them next to the sources
# would let the two disagree, and deno trusts meta.json.
echo "== hosted: publish the package's files =="
SCOPE="easyblobs"
PKG="hello"
VER="1.0.0"

mkdir -p "$WORK/pkg"
printf '%s\n' \
  '{' \
  '  "name": "@'"$SCOPE/$PKG"'",' \
  '  "version": "'"$VER"'",' \
  '  "exports": "./mod.ts"' \
  '}' > "$WORK/pkg/deno.json"
printf '%s\n' 'export function greet(): string { return "ez hello"; }' \
  > "$WORK/pkg/mod.ts"
printf '%s\n' '# easyblobs e2e' > "$WORK/pkg/README.md"

for f in deno.json mod.ts README.md; do
  code=$(curl -sS -o /tmp/jsr-up.out -w '%{http_code}' -X PUT \
    --data-binary "@$WORK/pkg/$f" \
    "$BASE/_hosted/e2e/$SCOPE/$PKG/$VER/$f")
  case "$code" in
    200|201) echo "   PUT $f -> $code" ;;
    *) fail "PUT $f -> $code $(cat /tmp/jsr-up.out)" ;;
  esac
done

echo "== hosted: the GENERATED meta.json lists the version =="
# deno addresses JSR packages as @scope/name. The hosted store keys files
# without the sigil, so BOTH forms are probed here and whichever answers is
# recorded - a mismatch between them is exactly the bug this caught last round
# (meta.json resolved, the module it described did not).
get_first() {
  # get_first <out> <path...>: fetch the first path that answers 200.
  _out="$1"; shift
  for _p in "$@"; do
    _code=$(curl -s -o "$_out" -w '%{http_code}' "$BASE/_hosted/e2e/$_p" 2>/dev/null || echo 000)
    if [ "$_code" = "200" ]; then
      echo "   $_p -> 200"
      echo "$_p" > "$WORK/lastpath"
      return 0
    fi
    echo "   $_p -> $_code"
  done
  return 1
}
get_first "$WORK/hmeta.json" "@$SCOPE/$PKG/meta.json" "$SCOPE/$PKG/meta.json" ||
  fail "the generated meta.json answers at neither path form"
cat "$WORK/hmeta.json" | sed 's/^/   /'
grep -q "\"$VER\"" "$WORK/hmeta.json" ||
  fail "meta.json does not list $VER"
grep -q "\"scope\": \"$SCOPE\"" "$WORK/hmeta.json" ||
  fail "meta.json has the wrong scope"

echo "== hosted: the GENERATED _meta.json carries a real checksum =="
get_first "$WORK/hvmeta.json" "@$SCOPE/$PKG/${VER}_meta.json" "$SCOPE/$PKG/${VER}_meta.json" ||
  fail "the generated ${VER}_meta.json answers at neither path form"
grep -q '"manifest"' "$WORK/hvmeta.json" || fail "_meta.json has no manifest"
# The checksum deno verifies must be the sha256 of the bytes we uploaded.
MODSUM=$(sha256sum "$WORK/pkg/mod.ts" | cut -d' ' -f1)
grep -q "sha256-$MODSUM" "$WORK/hvmeta.json" ||
  { echo "jsr: _meta.json does not carry the module's real sha256 ($MODSUM):" >&2
    cat "$WORK/hvmeta.json" >&2
    exit 1; }
echo "   checksum sha256-$MODSUM matches the uploaded module"
grep -q '"exports"' "$WORK/hvmeta.json" || fail "_meta.json has no exports map"
grep -q '"moduleGraph1"' "$WORK/hvmeta.json" || fail "_meta.json has no moduleGraph1"

echo "== hosted: the module bytes are served =="
get_first "$WORK/hmod.ts" "@$SCOPE/$PKG/$VER/mod.ts" "$SCOPE/$PKG/$VER/mod.ts" ||
  fail "the module answers at neither path form"
cmp -s "$WORK/hmod.ts" "$WORK/pkg/mod.ts" || fail "the served module differs"
echo "   bytes match"

echo "== hosted: DENO INSTALLS AND RUNS IT FROM THE HOSTED REPO =="
# This is the assertion the whole generator exists for: not "the documents
# parse" but "a client can install and execute from them".
export DENO_DIR="$WORK/deno-hosted"
mkdir -p "$DENO_DIR"
printf '%s\n' \
  "import { greet } from \"jsr:@$SCOPE/$PKG@$VER\";" \
  'console.log(greet());' > "$WORK/hosted.ts"

# The server logs every request (EASYBLOBS_DEBUG=1 in the job). Snapshot the
# log's line count before deno runs, then print what deno actually asked for:
# "package not found" from deno, with every mirror-side assertion passing, means
# it asked for a URL the mount does not answer - and the only way to know which
# is to look.
SRVLOG=/tmp/easyblobs.log
before=0
if [ -f "$SRVLOG" ]; then
  before=$(wc -l < "$SRVLOG")
fi

if DENO_REGISTRY_URL="$BASE/_hosted/e2e" \
   deno run -q "$WORK/hosted.ts" >/tmp/jsr-hosted.log 2>&1; then
  cat /tmp/jsr-hosted.log | sed 's/^/   /'
  grep -q "ez hello" /tmp/jsr-hosted.log ||
    fail "deno installed the package but its function did not run"
  echo "   deno installed it from the hosted repo and ran it"
else
  echo "jsr: deno could not install from the hosted repo:" >&2
  tail -25 /tmp/jsr-hosted.log >&2
  # What did deno actually request? The server saw it.
  if [ -f "$SRVLOG" ]; then
    echo "jsr: requests the server received while deno ran:" >&2
    tail -n +$((before + 1)) "$SRVLOG" | grep -iE '_hosted|jsr' | tail -25 >&2 ||
      echo "   (none mentioned _hosted or jsr)" >&2
  fi
  fail "deno run (hosted)"
fi

echo "== hosted: DELETE removes a file =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/_hosted/e2e/$SCOPE/$PKG/$VER/mod.ts")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/e2e/$SCOPE/$PKG/$VER/mod.ts" >/dev/null 2>&1; then
      fail "the deleted module is still served"
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "jsr: OK (pull: meta version-meta module deno-runs)"
echo "jsr: OK (hosted: publish generated-meta generated-checksum deno-installs delete)"
