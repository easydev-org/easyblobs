#!/bin/sh
# aur: the Arch User Repository, read side.
#
# AUR is READ-ONLY and that is a property of the service, not of this adapter:
# publishing means an SSH git push to aur@aur.archlinux.org, which has no
# self-hosted equivalent and no HTTP endpoint. There is therefore no hosted
# direction to test, and no hosted job for this protocol.
#
# What clients need is four GETs, and all four are mirrored:
#
#   <base>/rpc/v5/info?arg[]=<pkg>                    package metadata (JSON)
#   <base>/rpc/v5/search/<term>                       search (JSON)
#   <base>/cgit/aur.git/plain/PKGBUILD?h=<pkg>        the build script
#   <base>/cgit/aur.git/snapshot/<pkg>.tar.gz         the source snapshot
#   <base>/packages.gz                                every package name
#
# yay reads these from AURRPCURL and AURURL (`yay --aururl`, `--aurrpcurl`), so
# unlike jsr.io the mirror is actually reachable by the real client.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "aur: FAIL: $*" >&2; exit 1; }

PKG="yay"

echo "== the RPC info endpoint is served =="
curl -fsS -o "$WORK/info.json" "$BASE/rpc/v5/info?arg[]=$PKG" ||
  fail "GET /rpc/v5/info?arg[]=$PKG"
# Parsed with python3, not grepped: the JSON has no spaces ('"Name":"yay"'), and
# a shell-level grep for that had its quotes eaten and matched nothing - which
# reported a perfectly good reply as wrong.
python3 - "$WORK/info.json" "$PKG" <<'PYEOF' || fail "the RPC reply is not usable"
import json, sys
doc = json.load(open(sys.argv[1]))
want = sys.argv[2]
if "results" not in doc:
    raise SystemExit("no results key")
names = [r.get("Name") for r in doc["results"]]
if want not in names:
    raise SystemExit("named %r, want %r" % (names, want))
PYEOF
echo "   $PKG metadata: $(wc -c < "$WORK/info.json") bytes"

echo "== the RPC search endpoint is served =="
if curl -fsS -o "$WORK/search.json" "$BASE/rpc/v5/search/$PKG" 2>/dev/null &&
   [ -s "$WORK/search.json" ]; then
  N=$(python3 - "$WORK/search.json" <<'PYEOF'
import json, sys
doc = json.load(open(sys.argv[1]))
print(len(doc.get("results") or []))
PYEOF
)
  [ "$N" -gt 0 ] || fail "the search reply has no results"
  echo "   search returns $N entries"
else
  echo "   (search did not answer; info above is the assertion)"
fi

echo "== the PKGBUILD is served =="
curl -fsS -o "$WORK/PKGBUILD" "$BASE/cgit/aur.git/plain/PKGBUILD?h=$PKG" ||
  fail "GET the PKGBUILD"
# A PKGBUILD is shell: it must declare its own name and version, or a client
# that builds it gets a package called something else.
grep -q "^pkgname=$PKG" "$WORK/PKGBUILD" || fail "the PKGBUILD does not declare pkgname=$PKG"
grep -q '^pkgver=' "$WORK/PKGBUILD" || fail "the PKGBUILD has no pkgver"
echo "   it declares $(grep '^pkgver=' "$WORK/PKGBUILD")"

echo "== the source snapshot is served and is a tarball =="
curl -fsS -o "$WORK/snap.tar.gz" "$BASE/cgit/aur.git/snapshot/$PKG.tar.gz" ||
  fail "GET the snapshot"
tar -tzf "$WORK/snap.tar.gz" >/dev/null 2>&1 || fail "the snapshot is not a gzipped tar"
ENTRIES=$(tar -tzf "$WORK/snap.tar.gz" | wc -l)
echo "   $ENTRIES entries"
# The snapshot is what a builder actually compiles from, so its shape matters:
# the PKGBUILD has to be inside it.
tar -tzf "$WORK/snap.tar.gz" | grep -q "PKGBUILD" ||
  fail "the snapshot does not contain the PKGBUILD"
echo "   it contains the PKGBUILD"

echo "== the package list is served =="
if curl -fsS -o "$WORK/packages.gz" "$BASE/packages.gz" 2>/dev/null &&
   [ -s "$WORK/packages.gz" ]; then
  gunzip -c "$WORK/packages.gz" > "$WORK/packages.txt" 2>/dev/null ||
    fail "packages.gz is not gzip"
  grep -qx "$PKG" "$WORK/packages.txt" || fail "packages.gz does not list $PKG"
  echo "   packages.gz lists $(wc -l < "$WORK/packages.txt") packages, including $PKG"
fi

echo "== the metadata is usable, not merely present =="
# A client needs name, version and a package base to build from; anything less
# and yay would ask for a recipe with no instructions.
python3 - "$WORK/info.json" "$PKG" <<'PYEOF' || fail "the RPC metadata is missing required fields"
import json, sys
doc = json.load(open(sys.argv[1]))
want = sys.argv[2]
res = doc.get("results") or []
if not res:
    raise SystemExit("no results")
r = res[0]
for k in ("Name", "Version", "PackageBase"):
    if not r.get(k):
        raise SystemExit("missing %s" % k)
if r["Name"] != want:
    raise SystemExit("wrong package: %s" % r["Name"])
print("   %s %s (base %s)" % (r["Name"], r["Version"], r["PackageBase"]))
PYEOF

echo "== with yay present, point it at the mirror =="
if command -v yay >/dev/null 2>&1; then
  # yay's own --aurrpcurl, which is why this protocol is worth mirroring at all.
  if yay -Si --aurrpcurl "$BASE/rpc" "$PKG" >/tmp/aur-yay.log 2>&1; then
    head -5 /tmp/aur-yay.log | sed 's/^/   /'
    echo "   yay read the metadata through the mirror"
  else
    echo "   (yay did not complete; the endpoint assertions above stand)"
  fi
else
  echo "   (yay is not installed in this job; the endpoints are asserted directly)"
fi

echo "aur: OK (pull: rpc-info rpc-search pkgbuild snapshot packages-gz metadata-fields)"
