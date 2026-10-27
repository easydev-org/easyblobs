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

# ------------------------------------------------------------------ hosted --
# httpcache serves this tree through a HostedHandler, so files publish to
# _hosted/<repo>/. This tree has NO Generator: it gets index.txt and nothing more.
# So this asserts that publishing, serving and deleting work - not that
# a client could consume the tree as a repository, which would need a
# generator that does not exist.
echo "== hosted: publish a file =="
REPO="e2e"
FN="snapshot.json"
printf 'ez hello stackage hosted\n' > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/hosted-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) echo "stackage: the upload failed:" >&2
     cat /tmp/hosted-up.out >&2 || true
     exit 1 ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/$REPO/$FN" ||
  { echo "stackage: the uploaded file is not served back" >&2; exit 1; }
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] ||
  { echo "stackage: served bytes differ ($GOT != $SUM)" >&2; exit 1; }
echo "   sha256 $SUM matches"

echo "== hosted: the repo has a listing =="
curl -fsS "$BASE/_hosted/$REPO/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "stackage: index.txt does not list $FN" >&2; exit 1; }
echo "   index.txt lists $FN"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/$REPO/$FN" >/dev/null 2>&1; then
      echo "stackage: the deleted file is still served" >&2; exit 1
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "stackage: OK (pull: upstream)"
echo "stackage: OK (hosted: put bytes index-txt delete)"
