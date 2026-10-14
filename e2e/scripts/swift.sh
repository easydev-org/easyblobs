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
mkdir -p /w && cd /w
cat > main.swift <<'X'
import Foundation
let d = ["ok": true]
let j = try! JSONSerialization.data(withJSONObject: d)
print("swift + foundation: " + String(data: j, encoding: .utf8)!)
X
swiftc main.swift -o /w/app
/w/app
