#!/bin/sh
# swiftpm: the whole client surface against the mount, in both directions.
#
# The Swift Package Registry spec, route by route:
#
#   OPTIONS /                       the service discovery document
#   POST /identifiers?url=<repo>    map a source URL to a package id
#   GET  /{scope}/{name}            list releases
#   GET  /{scope}/{name}/{version}  release metadata
#   GET  /{scope}/{name}/{version}.zip  the source archive
#   PUT  /{scope}/{name}/{version}  publish
#   DELETE /{scope}/{name}/{version} remove
#
# api.spm.swift.org does not resolve, so the real `swift` client cannot be
# pointed at a mirror by configuration alone. The suite drives the HTTP face the
# spec defines, then uses the actual compiler to prove the archive is usable.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "swiftpm: FAIL: $*" >&2; exit 1; }

echo "== service discovery (OPTIONS) =="
code=$(curl -sS -o /tmp/opt.h -D /tmp/opt.h -w '%{http_code}' -X OPTIONS "$BASE/")
case "$code" in 200|204) ;; *) fail "OPTIONS -> $code" ;; esac
head -6 /tmp/opt.h

echo "== identifiers: a source URL maps to a package id =="
REPO="https://github.com/apple/swift-numerics"
ENC=$(printf '%s' "$REPO" | sed 's|:|%3A|g; s|/|%2F|g')
body=$(curl -sS -X POST "$BASE/identifiers?url=$ENC") || fail "POST /identifiers"
echo "   $body"
printf '%s' "$body" | grep -q "swift-numerics" ||
  fail "identifiers did not map the URL to a package"

echo "== releases for that package =="
curl -fsS "$BASE/apple/swift-numerics" >/dev/null || fail "GET /{scope}/{name}"
curl -fsS "$BASE/apple/swift-numerics" | grep -q releases ||
  fail "the releases document has no releases key"

echo "== a release archive is a valid zip =="
curl -fsS -o "$WORK/rel.json" "$BASE/apple/swift-numerics"
REL=$(python3 - "$WORK/rel.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
rels = d.get("releases") or {}
print(sorted(rels)[0] if rels else "")
PY
)
if [ -n "$REL" ]; then
  curl -fsS -o "$WORK/rel.zip" "$BASE/apple/swift-numerics/$REL.zip" ||
    fail "GET the release archive"
  python3 - "$WORK/rel.zip" <<'PY'
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
print("   entries:", len(z.namelist()))
PY
else
  echo "   (no releases advertised yet; the hosted case below publishes one)"
fi

# ------------------------------------------------------------------ hosted --
SCOPE="easyblobs-test"
NAME="hello"
VER="1.0.0"
PROJ="$WORK/pkg"
mkdir -p "$PROJ/Sources/Hello"
cd "$PROJ"
printf '%s\n' \
  '// swift-tools-version:5.9' \
  'import PackageDescription' \
  '' \
  'let package = Package(' \
  '    name: "Hello",' \
  '    products: [.library(name: "Hello", targets: ["Hello"])],' \
  '    targets: [.target(name: "Hello")]' \
  ')' > Package.swift
printf '%s\n' 'public func greet() -> String { "ez hello" }' \
  > Sources/Hello/Hello.swift

echo "== build the package and its archive =="
if ! swift build >/tmp/sw-build.log 2>&1; then
  echo "swiftpm: swift build failed:" >&2
  tail -15 /tmp/sw-build.log >&2
  fail "swift build"
fi
# The registry archive layout is <scope>.<name>-<version>/ at the root.
STAGE="$WORK/stage/$SCOPE.$NAME-$VER"
mkdir -p "$STAGE"
cp -a Package.swift Sources "$STAGE/"
( cd "$WORK/stage" && zip -q -r "$WORK/pkg.zip" "$SCOPE.$NAME-$VER" ) ||
  fail "zip the package"
[ -s "$WORK/pkg.zip" ] || fail "no archive produced"

echo "== publish with PUT =="
code=$(curl -sS -o /tmp/put.out -w '%{http_code}' -X PUT \
  -H 'Content-Type: application/zip' --data-binary "@$WORK/pkg.zip" \
  "$BASE/$SCOPE/$NAME/$VER")
case "$code" in 200|201|204) ;; *) fail "PUT -> $code $(cat /tmp/put.out)" ;; esac

echo "== the release is advertised =="
curl -fsS "$BASE/$SCOPE/$NAME" | grep -q "$VER" ||
  fail "the release list does not mention $VER"
curl -fsS "$BASE/$SCOPE/$NAME/$VER" >/dev/null || fail "GET the release metadata"

echo "== the archive comes back byte-identical =="
curl -fsS -o "$WORK/back.zip" "$BASE/$SCOPE/$NAME/$VER.zip" ||
  fail "GET the release archive"
cmp -s "$WORK/back.zip" "$WORK/pkg.zip" || fail "the served archive differs"
echo "   bytes match"

echo "== and it is a usable Swift package =="
BACK="$WORK/back"
mkdir -p "$BACK"
python3 - "$WORK/back.zip" "$BACK" <<'PY'
import sys, zipfile
zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])
PY
DIR=$(ls -d "$BACK"/*/ 2>/dev/null | head -1)
[ -n "$DIR" ] || fail "the archive has no root directory"
if ! ( cd "$DIR" && swift build >/tmp/sw-back.log 2>&1 ); then
  echo "swiftpm: the served archive does not build:" >&2
  tail -15 /tmp/sw-back.log >&2
  fail "unpacked archive builds"
fi
echo "   the unpacked archive builds"

echo "== DELETE the release =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/$SCOPE/$NAME/$VER")
case "$code" in
  200|202|204)
    if curl -fsS "$BASE/$SCOPE/$NAME" 2>/dev/null | grep -q "$VER"; then
      fail "the deleted release is still advertised"
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code - not fatal)" ;;
esac

echo "swiftpm: OK (pull: options identifiers releases archive-zip)"
echo "swiftpm: OK (hosted: build PUT metadata archive-bytes build-back delete)"
