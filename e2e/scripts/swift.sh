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
  # Where the compiler lives, and what is actually beside it. A link failure for
  # -lswiftCore is a broken toolchain, not a broken program, and the two have
  # different fixes - so say which one this is instead of leaving the linker's
  # output to be read as a client bug.
  SWIFTC=$(readlink -f "$(command -v swiftc)")
  ROOT=$(dirname "$(dirname "$SWIFTC")")
  echo "swift: swiftc = $SWIFTC" >&2
  echo "swift: prefix = $ROOT" >&2
  for d in "$ROOT/lib/swift/linux" "$ROOT/lib/swift" "$ROOT/lib"; do
    if [ -d "$d" ]; then
      n=$(ls "$d" 2>/dev/null | wc -l)
      core=$(ls "$d" 2>/dev/null | grep -c '^libswiftCore' || true)
      echo "swift: $d -> $n entries, libswiftCore*: $core" >&2
      [ "$core" = 0 ] && ls "$d" 2>/dev/null | head -8 >&2
    else
      echo "swift: $d does not exist" >&2
    fi
  done
  find "$ROOT" -name 'libswiftCore*' 2>/dev/null | head -3 >&2 || true
  echo "swift: if libswiftCore is nowhere under the prefix, the closure ships the" >&2
  echo "swift: compiler without its runtime libraries - an easysetup packaging gap," >&2
  echo "swift: not something this suite or the mirror can fix." >&2
  exit 1
fi
"$W/app"
