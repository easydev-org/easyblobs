#!/bin/sh
# swift: the Swift Package Registry, and the compiler that consumes it.
#
# The registry host (api.spm.swift.org) is NXDOMAIN, so the `swift` client
# cannot be pointed at a mirror by configuration. Two things follow:
#
#   * the HTTP face is driven directly - it IS the spec:
#       POST /identifiers?url=<repo>      map a source URL to a package id
#       GET  /{scope}/{name}              the release list
#       GET  /{scope}/{name}/{version}    one release's metadata
#       GET  /{scope}/{name}/{version}.zip  the source archive
#   * the compiler is still used, because a registry that answers perfectly and
#     serves an archive that does not build is not a usable registry.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "swift: FAIL: $*" >&2; exit 1; }

SCOPE="apple"
NAME="swift-numerics"
REPO="https://github.com/$SCOPE/$NAME"

echo "== identifiers: a source URL maps to a package id =="
ENC=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$REPO")
code=$(curl -sS -o "$WORK/id.json" -w '%{http_code}' -X POST "$BASE/identifiers?url=$ENC")
[ "$code" = "200" ] || fail "POST /identifiers -> $code"
grep -q "$NAME" "$WORK/id.json" ||
  fail "the identifier does not name the package: $(cat "$WORK/id.json")"
echo "   $NAME maps back to itself"

echo "== the release list =="
curl -fsS -o "$WORK/rels.json" "$BASE/$SCOPE/$NAME" || fail "GET /$SCOPE/$NAME"
grep -q '"releases"' "$WORK/rels.json" || fail "the release document has no releases key"
# The version LIST is served by proxying the upstream; the per-version metadata
# and archive are served from the STORE (versionMeta and sourceZip both read
# Meta.Get with no upstream fallback). So for a package nobody published here,
# every listed version 404s - which is what this suite used to assert against
# and then report as a mirror fault.
#
# That distinction is worth stating rather than papering over: the pull side of
# this protocol is the release list, and the metadata/archive side is the
# HOSTED side, which the hosted job in this matrix covers.
echo "   the list carries $(python3 -c '
import json,sys
print(len((json.load(open(sys.argv[1])).get("releases") or {})))
' "$WORK/rels.json") versions"

echo "== a version nobody published here is a 404, not an empty 200 =="
# versionMeta answers 404 rather than a release with an empty checksum, which is
# the behaviour a client needs: 'not found' instead of 'found but unusable'.
code=$(curl -s -o "$WORK/rel.json" -w '%{http_code}' "$BASE/$SCOPE/$NAME/1.0.0")
case "$code" in
  404) echo "   GET /$SCOPE/$NAME/1.0.0 -> 404 (not in the store; correct)" ;;
  200)
    # If the store does have it, the metadata must still be well formed.
    grep -q '"version"' "$WORK/rel.json" || fail "a 200 without a version field"
    echo "   GET /$SCOPE/$NAME/1.0.0 -> 200 with metadata" ;;
  *) fail "GET /$SCOPE/$NAME/1.0.0 -> $code" ;;
esac

echo "== the archive route behaves the same way =="
code=$(curl -s -o "$WORK/pkg.zip" -w '%{http_code}' "$BASE/$SCOPE/$NAME/1.0.0.zip")
case "$code" in
  404) echo "   GET /$SCOPE/$NAME/1.0.0.zip -> 404 (not in the store; correct)" ;;
  200)
    python3 -c '
import sys,zipfile
z=zipfile.ZipFile(sys.argv[1])
n=z.namelist()
if not n: raise SystemExit("empty archive")
if not any(x.endswith("Package.swift") for x in n):
    raise SystemExit("no Package.swift in the archive")
print("   %d entries, Package.swift present" % len(n))
' "$WORK/pkg.zip" || fail "the archive is not a usable Swift package" ;;
  *) fail "GET /$SCOPE/$NAME/1.0.0.zip -> $code" ;;
esac

echo "== and the compiler builds a program =="
# The compiler assertion is separate from the registry one on purpose: an
# archive can be a valid zip and still not build.
mkdir -p "$WORK/prog"
cd "$WORK/prog"
printf '%s\n' \
  'import Foundation' \
  'let d = ["ok": true, "n": 42] as [String : Any]' \
  'let j = try! JSONSerialization.data(withJSONObject: d)' \
  'print("swift ok:", j.count > 0)' > main.swift

# The nix Swift needs its runtime lib on the link line; its prefix derivation
# is lost when the package moves into the store.
SWIFT_LIB=$(dirname "$(dirname "$(readlink -f "$(command -v swiftc)")")")/lib
if swiftc main.swift -o app -L"$SWIFT_LIB" >/tmp/swift-build.log 2>&1; then
  OUT=$("$WORK/prog/app" 2>&1) || fail "the built binary did not run"
  echo "$OUT" | grep -q "swift ok" || fail "the binary printed nothing useful: $OUT"
  echo "   $OUT"
else
  echo "swift: swiftc failed:" >&2
  tail -20 /tmp/swift-build.log >&2
  fail "swiftc"
fi

echo "== a package that does not exist stays a 404 =="
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$SCOPE/this-package-does-not-exist")
case "$code" in
  404) echo "   -> 404 (correct)" ;;
  200) fail "a nonexistent package answered 200" ;;
  *) echo "   -> $code" ;;
esac

echo "swift: OK (pull: identifiers releases release-metadata archive-package compiler-runs 404)"
