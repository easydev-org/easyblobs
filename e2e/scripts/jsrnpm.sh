#!/bin/sh
# jsrnpm: JSR packages through the npm protocol.
#
# JSR publishes everything at npm.jsr.io under the @jsr scope, with the slash
# escaped as a double underscore (@jsr/luca__flag means jsr:@luca/flag). That
# endpoint speaks plain npm, so it is served by the npm adapter and tested here
# as npm - not as JSR.
#
# WHY NOT JSR'S NATIVE REGISTRY: deno hard-codes jsr.io. There is no DENO_*
# variable that redirects it - DENO_LOG=debug shows
#   FileFetcher::fetch_no_follow - specifier: https://jsr.io/@scope/pkg/meta.json
# whatever the environment says - so a mirror of the native protocol cannot be
# installed from by its only client. npm.jsr.io has no such problem: deno and
# npm both take a registry URL.
#
# The client here is DENO, because it reads npm registry config from DENO_NPM_REGISTRY
# and installs npm dependencies natively. A successful install means the
# packument parsed AND its dist.tarball was usable.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "jsrnpm: FAIL: $*" >&2; exit 1; }

command -v deno >/dev/null || { echo "jsrnpm: deno is missing"; exit 1; }
echo "jsrnpm: $(deno --version 2>&1 | head -1)"

PKG="@jsr/luca__flag"
VER="1.0.1"

echo "== the packument is served through the mount =="
curl -fsS -o "$WORK/packument.json" "$BASE/$PKG" || fail "GET /$PKG"
grep -q '"dist-tags"\|"versions"' "$WORK/packument.json" ||
  fail "the packument has neither dist-tags nor versions"
echo "   packument parses ($(wc -c < "$WORK/packument.json") bytes)"

echo "== the tarball the packument advertises is fetchable =="
# The dist.tarball URL is the thing a client actually needs; a packument whose
# tarball 404s installs nothing.
TB=$(python3 - "$WORK/packument.json" "$VER" <<'PYEOF'
import json, sys
doc = json.load(open(sys.argv[1]))
ver = sys.argv[2]
entry = (doc.get("versions") or {}).get(ver) or {}
print((entry.get("dist") or {}).get("tarball") or "")
PYEOF
)
if [ -n "$TB" ]; then
  # The advertised URL is absolute and points at the upstream host; rewrite its
  # origin to the mount so the fetch goes through the mirror, which is the round
  # trip under test.
  REL=$(printf '%s' "$TB" | sed 's|^https\?://[^/]*||')
  curl -fsS -o "$WORK/pkg.tgz" "$BASE$REL" || fail "the advertised tarball is not fetchable through the mount"
  tar -tzf "$WORK/pkg.tgz" >/dev/null 2>&1 || fail "the tarball is not a gzipped tar"
  echo "   fetched and verified the advertised tarball"
else
  echo "   (the packument named no tarball for $VER)"
fi

echo "== deno installs the package as an npm dependency =="
# DENO_NPM_REGISTRY is the documented way to point deno's npm resolution at a
# registry, unlike the JSR side which has no equivalent.
mkdir -p "$WORK/app"
printf '%s\n' \
  '{' \
  '  "imports": {' \
  '    "flagpkg": "npm:'"$PKG"'@'"$VER"'"' \
  '  }' \
  '}' > "$WORK/app/deno.json"
printf '%s\n' \
  'import * as mod from "flagpkg";' \
  'console.log("exports:", Object.keys(mod).join(","));' \
  > "$WORK/app/main.ts"

export DENO_DIR="$WORK/deno"
mkdir -p "$DENO_DIR"
if ( cd "$WORK/app" && DENO_NPM_REGISTRY="$BASE/" \
     deno run -q --allow-env main.ts ) >/tmp/jsrnpm.log 2>&1; then
  cat /tmp/jsrnpm.log | sed 's/^/   /'
  grep -q "exports:" /tmp/jsrnpm.log || fail "deno ran but imported nothing"
  echo "   deno installed and imported the npm-shaped JSR package"
else
  echo "jsrnpm: deno could not install $PKG through the mount:" >&2
  tail -25 /tmp/jsrnpm.log >&2
  fail "deno install"
fi

echo "jsrnpm: OK (pull: packument dist-tarball deno-imports)"
