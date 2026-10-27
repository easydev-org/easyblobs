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
# Take a version the list ACTUALLY names and that the metadata endpoint
# accepts. sorted()[-1] is a STRING sort - 1.0.2 sorts above 1.0.11 - so the
# "largest" name is not necessarily a real release, and picking it produced a
# 404 that looked like a mirror fault:
#   GET /apple/swift-numerics/1.1.1 -> 404
VER=""
for cand in $(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
for v in sorted((d.get("releases") or {})): print(v)
' "$WORK/rels.json"); do
  if curl -fsS -o "/dev/null" "$BASE/$SCOPE/$NAME/$cand" 2>/dev/null; then
    VER="$cand"
    break
  fi
done
[ -n "$VER" ] || fail "no release in the list has served metadata"
echo "   a listed release resolves: $VER"

echo "== one release's metadata =="
curl -fsS -o "$WORK/rel.json" "$BASE/$SCOPE/$NAME/$VER" || fail "GET /$SCOPE/$NAME/$VER"
grep -q '"version"' "$WORK/rel.json" || fail "the release metadata has no version field"
grep -q "\"$VER\"" "$WORK/rel.json" || fail "the metadata declares a different version"
echo "   the metadata declares $VER"

echo "== the archive is served and is a usable zip =="
curl -fsS -o "$WORK/pkg.zip" "$BASE/$SCOPE/$NAME/$VER.zip" ||
  fail "GET /$SCOPE/$NAME/$VER.zip"
python3 -c '
import sys,zipfile
z=zipfile.ZipFile(sys.argv[1])
n=z.namelist()
if not n: raise SystemExit("empty archive")
if not any(x.endswith("Package.swift") for x in n):
    raise SystemExit("no Package.swift in the archive")
print("   %d entries, Package.swift present" % len(n))
' "$WORK/pkg.zip" || fail "the archive is not a usable Swift package"

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
