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
# swiftc needs the Swift RUNTIME libraries, not just the compiler. Report which
# of the two is missing instead of letting the linker's message stand alone:
#   error: link command failed
#   cannot find -lswiftCore
# is a broken toolchain, not a broken program, and the fix is different.
if ! out=$(swiftc main.swift -o "$W/app" 2>&1); then
  echo "swift: swiftc failed:" >&2
  echo "$out" | tail -15 >&2
  libdir=$(dirname "$(readlink -f "$(command -v swiftc)")")/../lib/swift/linux
  if [ -d "$libdir" ]; then
    echo "swift: runtime libs present in $libdir:" >&2
    ls "$libdir" 2>/dev/null | head -5 >&2
  else
    echo "swift: NO runtime library directory at $libdir" >&2
    echo "swift: this is a toolchain packaging gap, not a client or mirror" >&2
    echo "swift: problem - the compiler is present but libswiftCore is not" >&2
  fi
  exit 1
fi
"$W/app"
