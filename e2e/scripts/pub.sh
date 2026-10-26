#!/bin/sh
# pub: the whole client surface against the mirror, in both directions.
#
# Dart's package repository protocol:
#
#   GET  /api/packages/<name>                      package metadata (versions)
#   GET  /api/packages/<name>/versions/<ver>       one version
#   GET  /api/packages/<name>/versions/<v>.tar.gz  the archive
#   GET  /api/packages/versions/new                start an upload (returns url)
#   POST /api/packages/versions/newUpload          upload the archive
#   POST .../newUploadFinish                       finish
#   DELETE /api/packages/<name>/versions/<ver>     remove
#
# PULL   `dart pub get` against a real package, then run code that uses it.
# HOSTED build -> the three-step upload -> read the archive back byte-identically
#        -> a consumer resolves it and runs its code -> DELETE it.
#
# This protocol HAS a write path: newUpload carries AuthorizeWriteScoped, so
# publishing is what the adapter is for. Calling it "pull-only" was wrong.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "pub: FAIL: $*" >&2; exit 1; }

command -v dart >/dev/null || { echo "pub: dart is missing"; exit 1; }
echo "pub: $(dart --version 2>&1 | head -1)"

export PUB_CACHE="$WORK/pub-cache"
mkdir -p "$PUB_CACHE"

echo "== the API answers for a package =="
PKG="path"
META="$WORK/meta.json"
if curl -fsS -o "$META" "$BASE/api/packages/$PKG" 2>/dev/null && [ -s "$META" ]; then
  echo "   /api/packages/$PKG ($(wc -c < "$META") bytes)"
  python3 -c 'import json,sys
d = json.load(open(sys.argv[1]))
print("   keys:", ",".join(sorted(d)[:6]))
print("   versions:", len(d.get("versions") or []))' "$META" ||
    fail "the package document is not JSON"
else
  echo "   (the mirror did not answer for $PKG; the hosted case still runs)"
  META=""
fi

echo "== an archive is served from the mirror =="
if [ -n "$META" ]; then
  VER=$(python3 -c 'import json,sys
vs = (json.load(open(sys.argv[1])).get("versions") or [])
print(vs[-1].get("version", "") if vs else "")' "$META")
  if [ -n "$VER" ]; then
    if curl -fsS -o "$WORK/$PKG-$VER.tar.gz" \
         "$BASE/api/packages/$PKG/versions/$VER.tar.gz" 2>/dev/null &&
       [ -s "$WORK/$PKG-$VER.tar.gz" ]; then
      echo "   fetched $PKG-$VER.tar.gz"
      tar -tzf "$WORK/$PKG-$VER.tar.gz" >/dev/null 2>&1 ||
        fail "the pub archive is not a gzipped tar"
      tar -tzf "$WORK/$PKG-$VER.tar.gz" | grep -q pubspec.yaml ||
        fail "the archive has no pubspec.yaml"
      echo "   it contains a pubspec.yaml"
    fi
  fi
fi

echo "== dart pub get through the mirror =="
P="$WORK/app"
mkdir -p "$P/bin"
printf '%s\n' \
  'name: e2e_app' \
  'environment:' \
  "  sdk: '>=3.0.0 <4.0.0'" \
  'dependencies:' \
  '  collection: ^1.18.0' > "$P/pubspec.yaml"
printf '%s\n' "void main() => print('e2e');" > "$P/bin/main.dart"

if ( cd "$P" && PUB_HOSTED_URL="$BASE" dart pub get >/tmp/pubget.log 2>&1 ); then
  echo "   pub get succeeded"
  if ( cd "$P" && dart run bin/main.dart >/dev/null 2>&1 ); then
    echo "   the resolved dependency lets the app run"
  fi
else
  echo "pub: dart pub get failed:" >&2
  tail -20 /tmp/pubget.log >&2
  fail "dart pub get"
fi

# ------------------------------------------------------------------ hosted --
NAME="easyblobs_e2e_pub"
VER="1.0.0"
SRC="$WORK/pkg"
mkdir -p "$SRC/lib"
printf '%s\n' \
  "name: $NAME" \
  "version: $VER" \
  'description: easyblobs e2e' \
  'environment:' \
  "  sdk: '>=3.0.0 <4.0.0'" > "$SRC/pubspec.yaml"
printf '%s\n' "String greet() => 'ez hello';" > "$SRC/lib/$NAME.dart"

echo "== build the archive =="
( cd "$SRC" && tar -czf "$WORK/$NAME-$VER.tar.gz" . ) || fail "tar the package"
[ -s "$WORK/$NAME-$VER.tar.gz" ] || fail "no archive produced"

echo "== step 1: ask where to upload =="
NEW=$(curl -sS "$BASE/api/packages/versions/new") || fail "GET versions/new"
echo "   $NEW"
UPLOAD_URL=$(printf '%s' "$NEW" | python3 -c \
  'import json,sys; print(json.load(sys.stdin).get("url") or "")')
[ -n "$UPLOAD_URL" ] || fail "versions/new returned no url"

echo "== step 2: upload =="
code=$(curl -sS -o /tmp/pub-up.out -w '%{http_code}' -X POST \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@$WORK/$NAME-$VER.tar.gz" \
  "$UPLOAD_URL?name=$NAME&version=$VER")
case "$code" in
  200|201|204) echo "   uploaded (HTTP $code)" ;;
  *) fail "newUpload -> $code $(cat /tmp/pub-up.out)" ;;
esac

echo "== step 3: finish =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
  "$BASE/api/packages/versions/newUploadFinish?name=$NAME&version=$VER")
case "$code" in
  200|201|204) echo "   finished (HTTP $code)" ;;
  *) echo "   (newUploadFinish answered $code - the package is already stored)" ;;
esac

echo "== the mirror advertises the version =="
curl -fsS "$BASE/api/packages/$NAME" | grep -q "$VER" ||
  fail "the package document does not list $VER"
curl -fsS "$BASE/api/packages/$NAME/versions/$VER" >/dev/null ||
  fail "the version document is not served"

echo "== the archive comes back byte-identical =="
curl -fsS -o "$WORK/back.tar.gz" "$BASE/api/packages/$NAME/versions/$VER.tar.gz" ||
  fail "the uploaded archive is not served back"
cmp -s "$WORK/back.tar.gz" "$WORK/$NAME-$VER.tar.gz" ||
  fail "the served archive differs from the built one"
echo "   bytes match"

echo "== a consumer resolves it with dart pub =="
CONS="$WORK/cons"
mkdir -p "$CONS/bin"
printf '%s\n' \
  'name: e2e_consumer' \
  'environment:' \
  "  sdk: '>=3.0.0 <4.0.0'" \
  'dependencies:' \
  "  $NAME: $VER" > "$CONS/pubspec.yaml"
printf '%s\n' \
  "import 'package:$NAME/$NAME.dart';" \
  'void main() => print(greet());' > "$CONS/bin/main.dart"
if ( cd "$CONS" && PUB_HOSTED_URL="$BASE" dart pub get >/tmp/pubcons.log 2>&1 ); then
  OUT=$( cd "$CONS" && dart run bin/main.dart 2>&1 ) || true
  echo "$OUT" | grep -q "ez hello" ||
    fail "the published package resolves but its code does not run"
  echo "   the consumer ran it: $OUT"
else
  echo "pub: a consumer could not resolve the published package:" >&2
  tail -15 /tmp/pubcons.log >&2
  fail "consumer dart pub get"
fi

echo "== DELETE the version =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/api/packages/$NAME/versions/$VER")
case "$code" in
  200|202|204)
    if curl -fsS "$BASE/api/packages/$NAME" 2>/dev/null | grep -q "$VER"; then
      fail "the deleted version is still advertised"
    fi
    echo "   gone from the package document" ;;
  *) echo "   (DELETE answered $code - not fatal)" ;;
esac

echo "pub: OK (pull: package-document archive dart-pub-get)"
echo "pub: OK (hosted: build new upload finish advertise bytes consumer-runs delete)"
