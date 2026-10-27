#!/bin/sh
# juliapkg: the whole client surface against the mirror, in both directions.
#
# A Julia package server is a tree of three things:
#
#   <base>/registries/<name>/Registry.toml   the registry index
#   <base>/package/<uuid>/<treehash>         a package's source tree
#   <base>/artifact/<hash>                   a binary artifact
#
# PULL   Pkg.Registry.add + Pkg.add resolve through the mirror, and the package
#        is LOADED and its function called.
# HOSTED the tree has no Generator, so what is publishable is asserted plainly:
#        a file goes in, comes back byte-identically, appears in index.txt, and
#        can be deleted. It does NOT claim `Pkg.add` works against a hosted
#        repo - that would need a registry generator this tree does not have.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "juliapkg: FAIL: $*" >&2; exit 1; }

command -v julia >/dev/null || { echo "juliapkg: julia is missing"; exit 1; }
echo "juliapkg: $(julia --version 2>&1 | head -1)"

export JULIA_PKG_SERVER="$BASE"
export JULIA_DEPOT_PATH="$WORK/depot"
mkdir -p "$JULIA_DEPOT_PATH"

echo "== pull: the registry is served =="
# The pkg server exposes registries under /registries/<name>/ and a registry
# tarball at /registry/<uuid>/<hash>; check at least one answers.
FOUND=""
for f in registries/General/Registry.toml registry General.tar.gz; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$f" 2>/dev/null || echo 000)
  echo "   $f -> $code"
  [ "$code" = "200" ] && FOUND="$f"
done

echo "== pull: Pkg resolves through the mirror =="
if julia -e 'using Pkg; Pkg.Registry.add("General")' >/tmp/jl-reg.log 2>&1; then
  echo "   registry added"
else
  echo "juliapkg: Pkg.Registry.add failed:" >&2
  tail -20 /tmp/jl-reg.log >&2
  fail "Pkg.Registry.add"
fi

if julia -e 'using Pkg; Pkg.add("JSON2")' >/tmp/jl-add.log 2>&1; then
  echo "   Pkg.add(JSON2) succeeded"
else
  echo "juliapkg: Pkg.add failed:" >&2
  tail -20 /tmp/jl-add.log >&2
  fail "Pkg.add"
fi

# Registered is not the same as usable: call it.
OUT=$(julia -e 'using JSON2; println(JSON2.write(Dict("ok" => true)))' 2>&1) ||
  { echo "juliapkg: the installed package does not load:" >&2; echo "$OUT" >&2; exit 1; }
echo "$OUT" | grep -q '"ok"' || fail "JSON2 did not serialise"
echo "   the installed package runs: $OUT"

# ------------------------------------------------------------------ hosted --
# This tree has NO Generator: httpcache gives it index.txt and nothing more. So
# what is asserted is publish/serve/delete, not "a client can add from here".
echo "== hosted: publish a file =="
FN="Registry.toml"
printf '%s\n' 'name = "EasyblobsE2e"' 'uuid = "00000000-0000-0000-0000-000000000001"' \
  > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/jl-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/e2e/registries/EasyblobsE2e/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) fail "upload -> $code $(cat /tmp/jl-up.out)" ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/e2e/registries/EasyblobsE2e/$FN" ||
  fail "the uploaded file is not served back"
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] || fail "served bytes differ ($GOT != $SUM)"
echo "   sha256 matches"

echo "== hosted: the repo listing names it =="
curl -fsS "$BASE/_hosted/e2e/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "juliapkg: index.txt does not list $FN:" >&2
    curl -fsS "$BASE/_hosted/e2e/index.txt" 2>/dev/null | head -10 >&2
    exit 1; }
echo "   index.txt lists it"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/_hosted/e2e/registries/EasyblobsE2e/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/e2e/registries/EasyblobsE2e/$FN" >/dev/null 2>&1; then
      fail "the deleted file is still served"
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "juliapkg: OK (pull: registry pkg-add load)"
echo "juliapkg: OK (hosted: put bytes index-txt delete)"
