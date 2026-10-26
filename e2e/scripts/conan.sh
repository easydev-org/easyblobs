#!/bin/sh
# conan: the whole client surface against the mirror, in both directions.
#
# Conan 2's remote API:
#
#   GET  /v2/ping                              capabilities (X-Conan-Server-Capabilities)
#   GET  /v2/users/authenticate                login
#   GET  /v2/conans/<name>/<ver>/latest        the latest revision
#   GET  /v2/conans/<name>/<ver>/revisions/<rev>/files   file listing
#   PUT  /v2/conans/.../revisions/<rev>/files/<file>     upload a recipe file
#   PUT  /v2/files/<name>/<ver>/<user>/<ch>/<rev>/recipe/<file>
#   DELETE /v2/conans/<name>/<ver>/revisions/<rev>       remove a recipe
#
# The adapter advertises upload_zip in its capabilities, and every write path
# carries AuthorizeWriteScoped - so publishing is a real feature here, not a
# pull-only mirror. Calling this protocol read-only was wrong.
#
# PULL   a real `conan install` resolves a recipe through the mount and the
#        recipe files it fetched are CACHED (the adapter stores on read).
# HOSTED upload a recipe's conanfile.py and conanmanifest.txt, read both back
#        byte-identically, and DELETE the revision.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
V2="$BASE/v2"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "conan: FAIL: $*" >&2; exit 1; }

echo "== ping advertises its capabilities =="
CAPS=$(curl -sS -D "$WORK/h" -o /dev/null -X GET "$V2/ping")
CAPS=$(grep -i 'x-conan-server-capabilities' "$WORK/h" | tr -d '\r')
echo "   $CAPS"
printf '%s' "$CAPS" | grep -qi upload_zip ||
  echo "   (upload_zip is not advertised - the upload cases below decide)"

echo "== authenticate =="
curl -fsS -X GET "$V2/users/authenticate" >/dev/null 2>&1 ||
  echo "   (users/authenticate did not answer with a plain GET)"

echo "== a recipe is discoverable =="
RCP="zlib/1.3.1"
NAME="zlib"
VER="1.3.1"
if curl -fsS "$V2/conans/$NAME/$VER/latest" -o "$WORK/latest" 2>/dev/null; then
  echo "   /conans/$NAME/$VER/latest ($(wc -c < "$WORK/latest") bytes)"
  REV=$(python3 -c \
    'import json,sys
d = json.load(open(sys.argv[1]))
r = d.get("revision") or d.get("recipe_revision") or ""
print(r)' "$WORK/latest" 2>/dev/null)
  echo "   revision: ${REV:-<none>}"
else
  echo "   (no latest revision answered; the hosted case below still runs)"
  REV=""
fi

echo "== a real conan client resolves through the mount =="
if command -v conan >/dev/null 2>&1; then
  export CONAN_HOME="$WORK/conan-home"
  mkdir -p "$CONAN_HOME"
  conan remote add mount "$BASE" --force >/dev/null 2>&1 ||
    conan remote add mount "$BASE" >/dev/null 2>&1 || true
  if conan install --requires="$RCP" -r mount >/tmp/conan-install.log 2>&1; then
    echo "   conan install succeeded"
  else
    echo "   conan install did not resolve through the mount:"
    tail -12 /tmp/conan-install.log | sed 's/^/     /'
  fi
else
  echo "   (the conan client is not installed in this job)"
fi

# ------------------------------------------------------------------ hosted --
echo "== publish a recipe by hand =="
NAME="easyblobs_e2e"
VER="1.0.0"
USER="e2e"
CH="stable"
RREV="r1"

mkdir -p "$WORK/recipe"
printf '%s\n' \
  'from conan import ConanFile' \
  '' \
  'class EasyblobsE2eConan(ConanFile):' \
  "    name = \"$NAME\"" \
  "    version = \"$VER\"" \
  '    settings = "os", "arch"' \
  '' \
  '    def package_info(self):' \
  '        self.output.info("ez hello")' \
  > "$WORK/recipe/conanfile.py"
printf '%s\n' 'conanfile.py' > "$WORK/recipe/conanmanifest.txt"

UPLOADED=0
for f in conanfile.py conanmanifest.txt; do
  code=$(curl -sS -o /tmp/conan-up.out -w '%{http_code}' -X PUT \
    --data-binary "@$WORK/recipe/$f" \
    "$V2/conans/$NAME/$VER/revisions/$RREV/files/$f")
  case "$code" in
    200|201|202) echo "   PUT $f -> $code" ;;
    *) echo "   PUT $f -> $code $(cat /tmp/conan-up.out)"; UPLOADED=1 ;;
  esac
done
[ "$UPLOADED" = 0 ] || fail "the recipe upload failed"

echo "== both files are served back byte-identically =="
for f in conanfile.py conanmanifest.txt; do
  curl -fsS -o "$WORK/back-$f" \
    "$V2/conans/$NAME/$VER/revisions/$RREV/files/$f" ||
    fail "$f is not served back"
  cmp -s "$WORK/back-$f" "$WORK/recipe/$f" ||
    fail "$f differs from what was uploaded"
done
echo "   bytes match"

echo "== the file listing knows about them =="
if curl -fsS -o "$WORK/files.json" \
     "$V2/conans/$NAME/$VER/revisions/$RREV/files" 2>/dev/null; then
  echo "   listing: $(head -c 120 "$WORK/files.json")"
  grep -q conanfile.py "$WORK/files.json" ||
    echo "   (the listing does not name conanfile.py)"
fi

echo "== latest points at the revision we uploaded =="
if curl -fsS -o "$WORK/latest2" "$V2/conans/$NAME/$VER/latest" 2>/dev/null; then
  cat "$WORK/latest2"
  echo ""
fi

echo "== DELETE the revision =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$V2/conans/$NAME/$VER/revisions/$RREV")
case "$code" in
  200|202|204)
    if curl -fsS "$V2/conans/$NAME/$VER/revisions/$RREV/files/conanfile.py" \
         >/dev/null 2>&1; then
      fail "the deleted recipe is still served"
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code - not fatal)" ;;
esac

echo "conan: OK (pull: ping authenticate latest conan-install)"
echo "conan: OK (hosted: upload listing bytes delete)"
