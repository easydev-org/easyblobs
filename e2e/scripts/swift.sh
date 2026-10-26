#!/bin/sh
# Swift Package Registry: point the client at the artifact mount. api.spm.swift.org
# is NXDOMAIN, so we exercise the adapter's HTTP face directly (identifiers +
# releases), then make the compiler build and run a real program.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
python3 - "$EASYBLOBS_BASE" <<'PY'
import sys, urllib.parse, urllib.request
base = sys.argv[1].rstrip("/")
def req(method, path):
    r = urllib.request.Request(base + path, method=method)
    with urllib.request.urlopen(r, timeout=120) as resp:
        return resp.status, resp.read()
repo = "https://github.com/apple/swift-numerics"
st, body = req("POST", "/identifiers?url=" + urllib.parse.quote(repo, safe=""))
assert st == 200, (st, body)
print("identifiers:", body[:100])
st, body = req("GET", "/apple/swift-numerics")
assert st == 200 and b"releases" in body, (st, body)
print("releases:", body[:140])
PY

# Compile/run assertion: build a Swift binary against the Foundation module.
#
# $E2E_W is the writable work dir ci-run.sh exports. This used to `mkdir -p /w`,
# which is a root-only path - on a normal runner the mkdir fails and every
# subsequent step runs in the wrong directory.
W="${E2E_W:-$(mktemp -d)}"
mkdir -p "$W" && cd "$W"
cat > main.swift <<'X'
import Foundation
let d = ["ok": true]
let j = try! JSONSerialization.data(withJSONObject: d)
print("swift + foundation: " + String(data: j, encoding: .utf8)!)
X
# swiftc needs -L for its runtime libraries. Once the toolchain is moved into
# /nix/store the compiler loses the prefix inference it normally does for itself,
# so it never looks in its own lib/ and the link fails:
#   ld.bfd: cannot find -lswiftCore: No such file or directory
#
# The libraries are in <prefix>/lib - libswiftCore.so sits beside the compiler's
# tree, NOT under lib/swift/linux (that holds the .swiftmodule interfaces). My
# earlier reading of this was wrong for exactly that reason: I checked
# lib/swift/linux and lib/swift, found no .so, and called it a packaging gap.
SWIFT_LIB="$(dirname "$(dirname "$(readlink -f "$(command -v swiftc)")")")/lib"
[ -d "$SWIFT_LIB" ] || { echo "swift: no $SWIFT_LIB beside swiftc" >&2; exit 1; }
if ! out=$(swiftc main.swift -o "$W/app" -L"$SWIFT_LIB" 2>&1); then
  echo "swift: swiftc failed even with -L$SWIFT_LIB:" >&2
  echo "$out" | tail -15 >&2
  echo "swift: libswiftCore* in that prefix:" >&2
  ls "$SWIFT_LIB" 2>/dev/null | grep '^libswiftCore' >&2 || echo "  (none)" >&2
  exit 1
fi
"$W/app"
