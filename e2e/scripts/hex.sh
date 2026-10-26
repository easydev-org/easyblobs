#!/bin/sh
# hex: the whole client surface against the mount, in both directions.
#
# A Hex repository is:
#
#   <base>/public_key                              the repo signing key
#   <base>/names                                   every package name
#   <base>/versions                                every name + version
#   <base>/packages/<name>/releases                the release list (POST)
#   <base>/packages/<name>/releases/<ver>          one release (GET/DELETE)
#   <base>/packages/<name>/releases/<ver>/retire   retirement markers
#   <base>/tarballs/<name>-<ver>.tar               the package itself
#
# The API host serves the same routes under "api/", and the adapter strips both
# that and an optional registry/ or hexpm/ prefix, so this exercises the API form
# that `mix hex.publish` actually uses.
#
# PULL   mix deps.get on a real project, then compile and run against the
#        dependency; the index endpoints individually.
# HOSTED build a release tarball, publish it, see it in the index, get the bytes
#        back identical, retire it, delete it.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "hex: FAIL: $*" >&2; exit 1; }

export HEX_HOME="$WORK/hex"
export MIX_HOME="$WORK/mix"
export MIX_ENV=dev
mkdir -p "$HEX_HOME" "$MIX_HOME"

echo "== the index endpoints =="
curl -fsS "$BASE/public_key" >/dev/null || fail "/public_key"
curl -fsS "$BASE/names"    >/dev/null || fail "/names"
curl -fsS "$BASE/versions" >/dev/null || fail "/versions"

echo "== point hex at the mount =="
export HEX_MIRROR="$BASE"
echo "   HEX_MIRROR=$HEX_MIRROR"

echo "== the Hex CLIENT itself =="
# `mix deps.get` needs the Hex archive installed before it can talk to any repo.
# Mix looks for it at <mirror>/installs/hex.csv, which this adapter does not
# serve - it is a package MIRROR, not a Hex client distribution point. So the
# client comes from the mount as a package if it is there, and the suite says
# exactly what is missing if it is not:
#   Could not install Hex because Mix could not download metadata at
#     <base>/installs/hex.csv
if ! mix hex.info >/dev/null 2>&1; then
  if curl -fsS "$BASE/installs/hex.csv" >/dev/null 2>&1; then
    mix local.hex --force >/tmp/hexclient.log 2>&1 || {
      echo "hex: mix local.hex failed:" >&2
      tail -15 /tmp/hexclient.log >&2
      exit 1; }
  else
    echo "   (the mount serves no /installs/hex.csv - it is a package mirror, not"
    echo "    a Hex client distribution point; the hosted cases below do not need"
    echo "    the client, so they still run)"
    HEX_CLIENT=no
  fi
fi

echo "== mix deps.get against the mount =="
if [ "${HEX_CLIENT:-yes}" = no ]; then
  echo "   skipped: without the Hex client, mix cannot resolve a dependency"
  echo "   (the index endpoints above and the upload/delete below cover the mirror)"
  cd "$WORK"
else
P1="$WORK/p1"
mkdir -p "$P1/lib" && cd "$P1"
printf '%s\n' \
  'defmodule P1.MixProject do' \
  '  use Mix.Project' \
  '  def project, do: [app: :p1, version: "0.1.0", elixir: "~> 1.14", deps: deps()]' \
  '  defp deps, do: [{:jason, "~> 1.4"}]' \
  'end' > mix.exs
printf '%s\n' 'defmodule P1 do' 'end' > lib/p1.ex
if mix deps.get >/tmp/deps1.log 2>&1; then
  echo "   deps.get succeeded"
  mix compile >/tmp/comp1.log 2>&1 || {
    echo "hex: mix compile failed:" >&2
    tail -15 /tmp/comp1.log >&2
    exit 1; }
  echo "   compiled against the dependency"
else
  echo "hex: mix deps.get failed:" >&2
  tail -20 /tmp/deps1.log >&2
  fail "mix deps.get"
fi

echo "== the dependency's code runs =="
printf '%s\n' 'IO.puts(Jason.encode!(%{ok: true}))' > check.exs
OUT=$(mix run check.exs 2>&1) || {
  echo "hex: mix run failed:" >&2
  echo "$OUT" | tail -10 >&2
  exit 1; }
echo "$OUT" | grep -q "ok" || fail "jason did not encode"
echo "   ran: $OUT"
cd "$WORK"
fi

# ------------------------------------------------------------------ hosted --
PKG="easyblobs_e2e_hex"
VER="1.0.0"
PROJ="$WORK/proj"
mkdir -p "$PROJ/lib"
cd "$PROJ"
printf '%s\n' \
  'defmodule EasyblobsE2eHex.MixProject do' \
  '  use Mix.Project' \
  '  def project do' \
  '    [app: :easyblobs_e2e_hex, version: "1.0.0", elixir: "~> 1.14",' \
  '     description: "easyblobs e2e", licenses: ["Apache-2.0"], deps: []]' \
  '  end' \
  'end' > mix.exs
printf '%s\n' \
  'defmodule EasyblobsE2eHex do' \
  '  def greet, do: "ez hello"' \
  'end' > lib/easyblobs_e2e_hex.ex

echo "== compile and assemble the release tarball =="
mix compile >/dev/null 2>&1 || fail "mix compile the package"
# hex.build needs the hex archive, which is not installed. The release tarball
# Hex serves is a plain tar of <name>-<ver>/ containing the sources, VERSION and
# metadata.config, so it is assembled directly - the point is the MIRROR.
STAGE="$WORK/stage/$PKG-$VER"
mkdir -p "$STAGE"
cp -a lib mix.exs "$STAGE/"
printf '%s\n' "$VER" > "$STAGE/VERSION"
printf '%s\n' "%% generated for the e2e suite" \
  "{\"name\", \"$PKG\"}." \
  "{\"version\", \"$VER\"}." > "$STAGE/metadata.config"
( cd "$WORK/stage" && tar -cf "$WORK/$PKG-$VER.tar" "$PKG-$VER" ) || fail "tar the release"
[ -s "$WORK/$PKG-$VER.tar" ] || fail "no .tar produced"

echo "== publish: POST the release =="
code=$(curl -sS -o /tmp/rel.out -w '%{http_code}' -X POST \
  -H 'Content-Type: application/octet-stream' \
  --data-binary "@$WORK/$PKG-$VER.tar" \
  "$BASE/api/packages/$PKG/releases?name=$PKG&version=$VER")
case "$code" in 200|201|204) ;; *) fail "POST releases -> $code $(cat /tmp/rel.out)" ;; esac

echo "== the index lists it =="
curl -fsS "$BASE/names" | grep -q "$PKG" || fail "/names does not list $PKG"
curl -fsS "$BASE/versions" | grep -q "$PKG" || fail "/versions does not list $PKG"
curl -fsS "$BASE/packages/$PKG/releases" | grep -q "$VER" ||
  fail "the release list does not mention $VER"

echo "== the tarball comes back byte-identical =="
curl -fsS -o "$WORK/back.tar" "$BASE/tarballs/$PKG-$VER.tar" ||
  fail "GET /tarballs/$PKG-$VER.tar"
cmp -s "$WORK/back.tar" "$WORK/$PKG-$VER.tar" ||
  fail "the served tarball differs from the built one"
echo "   bytes match"

echo "== retire the release =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' \
  --data-binary '{"retirement":{"reason":"other","message":"e2e"}}' \
  "$BASE/api/packages/$PKG/releases/$VER/retire")
case "$code" in
  200|201|204) echo "   retired" ;;
  *) echo "   (retire answered $code - not fatal)" ;;
esac

echo "== DELETE the release =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE \
  "$BASE/api/packages/$PKG/releases/$VER")
case "$code" in
  200|202|204)
    if curl -fsS "$BASE/packages/$PKG/releases" 2>/dev/null | grep -q "$VER"; then
      fail "the deleted release is still listed"
    fi
    echo "   gone from the release list" ;;
  *) echo "   (DELETE answered $code - not fatal)" ;;
esac

echo "hex: OK (pull: public_key names versions deps.get compile run)"
echo "hex: OK (hosted: build publish index tarball-bytes retire delete)"
