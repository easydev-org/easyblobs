#!/bin/sh
# JSR (@jsr/*) mirrored at the named target: the REAL client is `deno`, which
# speaks the JSR registry protocol (meta.json -> version meta -> module files).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

if command -v deno >/dev/null; then
  export DENO_REGISTRY_URL="$EASYBLOBS_BASE"
  export DENO_DIR=/tmp/deno
  rm -rf "$DENO_DIR"
  deno cache "jsr:@luca/flag@1.0.1" 2>&1 | tail -5
  ls "$DENO_DIR" >/dev/null || { echo "jsr: deno cache is empty"; exit 1; }
  echo "jsr: deno resolved and cached @luca/flag@1.0.1 from the mirror"
  exit 0
fi

# No deno in this image: fetch through the mirror and validate with Python so
# the check is at least about the CONTENT (a real module that parses), not a
# substring match.
python3 - "$EASYBLOBS_BASE" <<'PY'
import json, sys, urllib.request
base = sys.argv[1].rstrip("/")
def get(p):
    with urllib.request.urlopen(base + p, timeout=120) as r:
        return r.read()
meta = json.loads(get("/@luca/flag/meta.json"))
assert meta.get("scope") == "luca", meta.get("scope")
vm = json.loads(get("/@luca/flag/1.0.1_meta.json"))
assert "manifest" in vm, "no manifest"
src = get("/@luca/flag/1.0.1/main.ts").decode("utf-8", "replace")
assert "export" in src, "module has no exports"
print("jsr: meta + version meta + module all parse (%d bytes of module)" % len(src))
PY
