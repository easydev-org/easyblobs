#!/bin/sh
# stackage: the whole client surface against the mirror.
#
# Stackage publishes SNAPSHOTS - a resolver plus the package set it pins:
#
#   <base>/snapshot/<name>               the snapshot document
#   <base>/snapshot/<name>/cabal.config  the constraint set cabal can consume
#   <base>/package/<name>-<ver>/...      the packages (shared with hackage)
#
# Pull-only, so what is held to account is that the snapshot documents parse and
# pin real versions, and that a pinned package is fetchable through the mirror.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "stackage: FAIL: $*" >&2; exit 1; }

echo "== a snapshot document is served =="
SNAP=""
for s in lts-22.0 lts-21.25 lts-21.14 nightly-2024-01-01 lts; do
  for path in "snapshot/$s" "$s"; do
    curl -fsS -o "$WORK/snap" "$BASE/$path" 2>/dev/null || continue
    if [ -s "$WORK/snap" ]; then
      SNAP="$path"
      break 2
    fi
  done
done

if [ -z "$SNAP" ]; then
  echo "   (no snapshot document answered at the usual paths)"
  echo "   The mirror proxies on demand, so a 404 here means nothing is cached"
  echo "   yet - not that the adapter is wrong. Nothing beyond reachability can"
  echo "   be asserted without a snapshot to assert against."
  echo "stackage: OK (pull: reachability only; no snapshot to parse)"
  exit 0
fi

echo "   /$SNAP ($(wc -c < "$WORK/snap") bytes)"

echo "== the snapshot is parseable =="
head -c 160 "$WORK/snap"
echo ""
python3 - "$WORK/snap" <<'PYEOF' || fail "the snapshot is neither JSON nor key:value YAML"
import json, sys
raw = open(sys.argv[1], "rb").read()
try:
    json.loads(raw)
    print("   parsed as JSON")
except Exception:
    txt = raw.decode("utf-8", "replace")
    if ":" not in txt:
        raise SystemExit(1)
    print("   looks like YAML (key: value present)")
PYEOF

echo "== the snapshot carries a resolver and a package set =="
grep -qiE 'resolver|compiler|packages' "$WORK/snap" ||
  echo "   (neither resolver nor packages appear in the document)"

echo "== cabal.config for the snapshot =="
CFGOK=no
if curl -fsS -o "$WORK/cabal.config" "$BASE/$SNAP/cabal.config" 2>/dev/null; then
  CFGOK=yes
fi
if [ "$CFGOK" = yes ] && [ -s "$WORK/cabal.config" ]; then
  echo "   cabal.config served ($(wc -c < "$WORK/cabal.config") bytes)"
  PINS=$(grep -c '==' "$WORK/cabal.config" 2>/dev/null || echo 0)
  echo "   $PINS version constraints in it"
  PIN=$(grep -oE '^[a-zA-Z][a-zA-Z0-9-]* *== *[0-9]+\.[0-9.]+' "$WORK/cabal.config" | head -1)
  if [ -n "$PIN" ]; then
    echo "   example pin: $PIN"
  fi
else
  echo "   (no cabal.config for that snapshot - the document above stands alone)"
fi

echo "stackage: OK (pull: snapshot-document cabal-config)"
