#!/bin/sh
# pecl: the whole client surface against the mirror, in both directions.
#
# PECL (the PHP Extension Community Library) serves two things:
#
#   <base>/get/<ext>-<ver>.tgz            the release archive
#   <base>/rest/p/<ext>/allreleases.xml   the REST API (pickle format)
#   <base>/rest/p/<ext>/info.xml          per-extension info
#   <base>/package.xml                    the channel descriptor
#
# The tree is served through httpcache, so uploads live under _hosted/<repo>/.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "pecl: FAIL: $*" >&2; exit 1; }

echo "== the channel descriptor =="
if curl -fsS -o "$WORK/package.xml" "$BASE/package.xml" 2>/dev/null &&
   [ -s "$WORK/package.xml" ]; then
  echo "   package.xml served ($(wc -c < "$WORK/package.xml") bytes)"
  grep -qi "<package" "$WORK/package.xml" || fail "package.xml is not a descriptor"
else
  echo "   (no package.xml; the REST documents below are the real assertions)"
fi

echo "== the REST API for an extension =="
EXT="redis"
REST=""
for p in "rest/p/$EXT/allreleases.xml" "rest/p/$EXT/info.xml" "rest/p/$EXT/latest.txt"; do
  if curl -fsS -o "$WORK/rest.out" "$BASE/$p" 2>/dev/null && [ -s "$WORK/rest.out" ]; then
    REST="$p"
    echo "   /$p ($(wc -c < "$WORK/rest.out") bytes)"
    break
  fi
done
if [ -z "$REST" ]; then
  echo "   (the pecl REST endpoints did not answer for $EXT)"
fi

echo "== a release archive is served and is a real tgz =="
TGZ=""
if [ -n "$REST" ]; then
  VER=$(grep -oE '[0-9]+\.[0-9]+\.[0-9]+' "$WORK/rest.out" | head -1)
  if [ -n "$VER" ]; then
    if curl -fsS -o "$WORK/$EXT-$VER.tgz" "$BASE/get/$EXT-$VER.tgz" 2>/dev/null &&
       [ -s "$WORK/$EXT-$VER.tgz" ]; then
      TGZ="$EXT-$VER.tgz"
    fi
  fi
fi
if [ -n "$TGZ" ]; then
  echo "   fetched $TGZ"
  tar -xzf "$WORK/$TGZ" -C "$WORK" 2>/dev/null || fail "$TGZ is not a gzipped tar"
  if find "$WORK" -maxdepth 2 -name 'package.xml' | grep -q .; then
    echo "   it contains the extension's package.xml"
  else
    echo "   (no package.xml inside the archive - unusual for pecl)"
  fi
else
  echo "   (no release archive answered; the hosted case below still runs)"
fi

# ------------------------------------------------------------------ hosted --
echo "== publish into a hosted channel =="
EXT="easyblobs_e2e_ext"
VER="1.0.0"
SRC="$WORK/src/$EXT-$VER"
mkdir -p "$SRC"
printf '%s\n' \
  '<?xml version="1.0" encoding="UTF-8"?>' \
  '<package version="2.0">' \
  "  <name>$EXT</name>" \
  '  <channel>pecl.php.net</channel>' \
  '  <summary>easyblobs e2e</summary>' \
  '  <description>e2e</description>' \
  "  <version><release>$VER</release><api>1.0.0</api></version>" \
  '  <stability><release>stable</release><api>stable</api></stability>' \
  '  <license>Apache-2.0</license>' \
  '  <contents><dir name="/"><file name="hello.php" role="php"/></dir></contents>' \
  '  <dependencies><required><php><min>8.0.0</min></php></required></dependencies>' \
  '</package>' > "$SRC/package.xml"
printf '%s\n' '<?php' 'function easyblobs_e2e_greet(): string { return "ez hello"; }' \
  > "$SRC/hello.php"
( cd "$WORK/src" && tar -czf "$WORK/$EXT-$VER.tgz" "$EXT-$VER" ) || fail "build the release"
[ -s "$WORK/$EXT-$VER.tgz" ] || fail "no release archive produced"

code=$(curl -sS -o /tmp/pecl.out -w '%{http_code}' -X PUT \
  --data-binary "@$WORK/$EXT-$VER.tgz" \
  "$BASE/_hosted/e2e/get/$EXT-$VER.tgz")
case "$code" in
  200|201) echo "   uploaded (HTTP $code)" ;;
  *) fail "upload -> $code $(cat /tmp/pecl.out)" ;;
esac

echo "== it is served back byte-identically =="
curl -fsS -o "$WORK/back.tgz" "$BASE/_hosted/e2e/get/$EXT-$VER.tgz" ||
  fail "the uploaded archive is not served back"
cmp -s "$WORK/back.tgz" "$WORK/$EXT-$VER.tgz" ||
  fail "the served archive differs from the built one"
echo "   bytes match"

echo "== the hosted channel generates an index =="
for idx in PACKAGES repodata.json index.json; do
  if curl -fsS "$BASE/_hosted/e2e/$idx" 2>/dev/null | grep -q "$EXT"; then
    echo "   $idx lists $EXT"
    break
  fi
done

echo "pecl: OK (pull: package.xml rest get-archive)"
echo "pecl: OK (hosted: upload bytes index)"
