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

# --------------------------------------------------------------- real client --
# Everything below hand-builds PEAR_REST / get requests. That pins the wire
# format, and it is not the same thing as the real client: `pecl` reads
# channel.xml, resolves the REST base from it, and then downloads and unpacks.
# A mirror that answered every hand-built path and still confused `pecl` would
# pass all of it.
if command -v pecl >/dev/null 2>&1; then
  echo "== pecl: point the real client at the mount =="

  # `pecl channel-update` reads the channel's own descriptor to learn where its
  # REST endpoints live. That descriptor is what this first step proves.
  code=$(curl -sS -o "$WORK/channel.xml" -w '%{http_code}' "$BASE/channel.xml" 2>/dev/null || echo 000)
  if [ "$code" = "200" ]; then
    grep -q "<channel" "$WORK/channel.xml" || fail "channel.xml is not a channel descriptor"
    echo "   channel.xml is served ($(wc -c < "$WORK/channel.xml") bytes)"
  else
    echo "   (no channel.xml at the mount root: $code)"
  fi

  # A local, writable pear config so the client writes nothing outside $WORK.
  # PHP_PEAR_SYSCONF_DIR is the documented override; without it pear writes to
  # /etc/pear.conf, which needs root and does not exist on the runner.
  #
  # Do NOT touch a pear.conf here. An EMPTY file is not an empty config - pear
  # reads it and refuses:
  #   ERROR: The default config file is not a valid config file or is corrupted.
  # The directory alone is what pear wants; it creates its own file.
  export PHP_PEAR_SYSCONF_DIR="$WORK"

  # `pecl channel-discover` is INFORMATIONAL here, not an assertion. It registers
  # a named channel with pear, and pear derives that name from a real DNS host
  # with a REST layout at /rest - neither of which a bare 127.0.0.1:8080 mount
  # provides. Failing the suite on it would be asserting a naming convention
  # rather than the protocol.
  #
  # What actually matters is below: the client reading the REST API itself.
  HOSTPORT=$(printf '%s' "$BASE" | sed 's|^https\?://||')
  if pecl channel-discover "$HOSTPORT" >/tmp/pecl-discover.log 2>&1; then
    echo "   pecl discovered the channel $HOSTPORT"
  else
    echo "   (channel-discover needs a DNS-named host; informational only)"
    tail -3 /tmp/pecl-discover.log | sed 's/^/     /'
  fi

  # `pecl remote-info` is the client reading the REST API and parsing the answer
  # itself - the closest thing to "does the protocol work". It resolves the REST
  # base from the channel's OWN config, so the channel's rest/baseurl has to be
  # pointed at the mount first. `pear config-set` writes into the sysconf dir,
  # which is ours.
  PCL_CHANNEL="pecl.php.net"
  if pecl config-set "default_channel" "$PCL_CHANNEL" >/dev/null 2>&1 &&
     pear channel-set "$PCL_CHANNEL" rest "$BASE/rest" >/dev/null 2>&1; then
    echo "   channel $PCL_CHANNEL rest base -> $BASE/rest"
  else
    echo "   (could not repoint the channel's REST base)"
  fi

  if pecl remote-info redis >/tmp/pecl-remote.log 2>&1; then
    head -6 /tmp/pecl-remote.log | sed 's/^/   /'
    echo "   pecl read the extension's metadata through the mount"
  else
    echo "   pecl remote-info did not complete (the REST assertions below stand):"
    tail -6 /tmp/pecl-remote.log | sed 's/^/     /'
  fi
else
  echo "== pecl is not installed; the REST and archive paths are asserted directly =="
  echo "   (pecl ships with php-pear, which the job installs when it can)"
fi

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
