#!/bin/sh
# debian: the whole client surface against the mount, in both directions.
#
# An apt repository is:
#
#   <base>/<archive>/dists/<suite>/Release             the release file
#   <base>/<archive>/dists/<suite>/InRelease           the signed form
#   <base>/<archive>/dists/<suite>/Packages            the package index
#   <base>/<archive>/pool/<...>/<pkg>.deb              the packages
#   <base>/_hosted/<repo>/...                          a self-published repo
#
# PULL   apt-get update parses Release + Packages; apt-get install fetches a
#        .deb and unpacks it; the installed binary is then RUN.
# HOSTED publish a real .deb under _hosted, watch the generated Packages index
#        list it, install it back from that repo, and run what it installed.
#
# The hosted form matters because debian has GeneratePackages: the index is
# derived from what was uploaded, so "the package appears in Packages" is a real
# assertion about the adapter, not about a file we also wrote ourselves.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "debian: FAIL: $*" >&2; exit 1; }

# ------------------------------------------------------------------ pull ----
echo "== pull: point apt at the mount =="
mkdir -p /etc/apt/sources.list.d
rm -f /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list
printf 'deb [trusted=yes] %s/debian bookworm main\n' "$BASE" \
  > /etc/apt/sources.list.d/artifact.list

echo "== pull: the Release file is served =="
REL=""
for f in dists/bookworm/Release dists/bookworm/InRelease; do
  if curl -fsS -o "$WORK/rel" "$BASE/debian/$f" 2>/dev/null && [ -s "$WORK/rel" ]; then
    REL="$f"
    echo "   $f ($(wc -c < "$WORK/rel") bytes)"
    break
  fi
done
[ -n "$REL" ] || fail "no Release/InRelease in the archive"

echo "== pull: the Packages index is served =="
PKG=""
for f in dists/bookworm/main/binary-amd64/Packages \
         dists/bookworm/main/binary-amd64/Packages.gz; do
  if curl -fsS -o "$WORK/pkgs" "$BASE/debian/$f" 2>/dev/null && [ -s "$WORK/pkgs" ]; then
    PKG="$f"
    echo "   $f ($(wc -c < "$WORK/pkgs") bytes)"
    break
  fi
done
[ -n "$PKG" ] || fail "no Packages index in the archive"

echo "== pull: apt-get update =="
if apt-get update -o Acquire::Retries=0 >/tmp/apt-update.log 2>&1; then
  echo "   update succeeded"
else
  echo "debian: apt-get update failed:" >&2
  tail -20 /tmp/apt-update.log >&2
  fail "apt-get update"
fi

echo "== pull: install and RUN a package =="
apt-get install -y --no-install-recommends jq >/tmp/apt-install.log 2>&1 || {
  echo "debian: apt-get install jq failed:" >&2
  tail -20 /tmp/apt-install.log >&2
  exit 1; }
echo '{"ok":true}' | jq -e '.ok' >/dev/null || fail "the installed jq does not work"
echo "   jq runs: $(jq --version)"

# ------------------------------------------------------------------ hosted --
echo "== hosted: build a real .deb =="
PKGNAME="easyblobs-e2e"
VER="1.0.0"
ROOT="$WORK/debroot"
mkdir -p "$ROOT/DEBIAN" "$ROOT/usr/bin"
printf '%s\n' '#!/bin/sh' 'echo "ez hello"' > "$ROOT/usr/bin/ezhello"
chmod 755 "$ROOT/usr/bin/ezhello"
{
  echo "Package: $PKGNAME"
  echo "Version: $VER"
  echo "Architecture: amd64"
  echo "Maintainer: e2e <e2e@example.com>"
  echo "Description: easyblobs e2e"
} > "$ROOT/DEBIAN/control"

DEB="$PKGNAME.deb"
if command -v dpkg-deb >/dev/null 2>&1; then
  dpkg-deb --build --root-owner-group "$ROOT" "$WORK/$DEB" >/dev/null 2>&1 ||
    fail "dpkg-deb build"
else
  # ar + tar, which is what a .deb is.
  ( cd "$ROOT" && tar -cf "$WORK/data.tar" --exclude=DEBIAN . &&
    tar -cf "$WORK/ctrl.tar" -C DEBIAN . ) || fail "tar the deb payload"
  ( cd "$WORK" && ar rc "$DEB" ctrl.tar data.tar ) || fail "ar the deb"
fi
[ -s "$WORK/$DEB" ] || fail "no .deb produced"
file "$WORK/$DEB" 2>/dev/null | head -1

echo "== hosted: publish under _hosted/<repo>/ =="
code=$(curl -sS -o /tmp/deb-up.out -w '%{http_code}' -X PUT \
  --data-binary "@$WORK/$DEB" \
  "$BASE/_hosted/e2e/pool/main/${PKGNAME}_${VER}_amd64.deb")
case "$code" in 200|201) echo "   uploaded (HTTP $code)" ;;
  *) fail "upload -> $code $(cat /tmp/deb-up.out)" ;; esac

echo "== hosted: the generated index lists it =="
IDX=""
for f in Packages Packages.gz; do
  if curl -fsS -o "$WORK/hpkgs" "$BASE/_hosted/e2e/$f" 2>/dev/null && [ -s "$WORK/hpkgs" ]; then
    IDX="$f"
    break
  fi
done
if [ -n "$IDX" ]; then
  echo "   $IDX is generated"
  if grep -q "$PKGNAME" "$WORK/hpkgs" 2>/dev/null; then
    echo "   it lists $PKGNAME"
  else
    fail "the generated index does not list the uploaded package"
  fi
else
  echo "   (no generated index at the repo root; the install below decides)"
fi

echo "== hosted: install it back from the hosted repo =="
rm -f /etc/apt/sources.list.d/*.list /etc/apt/sources.list.d/*.sources
printf 'deb [trusted=yes] %s/_hosted/e2e ./\n' "$BASE" \
  > /etc/apt/sources.list.d/hosted.list
if apt-get update -o Acquire::Retries=0 >/tmp/apt-h.log 2>&1 &&
   apt-get install -y --no-install-recommends "$PKGNAME" >/tmp/apt-hi.log 2>&1; then
  echo "   installed from the hosted repo"
  command -v ezhello >/dev/null || fail "the package installed no ezhello"
  ezhello | grep -q "ez hello" || fail "the installed ezhello does not run"
  echo "   the installed binary runs"
else
  echo "debian: the hosted package could not be installed:" >&2
  tail -15 /tmp/apt-h.log >&2
  tail -15 /tmp/apt-hi.log >&2
  fail "hosted install"
fi

echo "debian: OK (pull: release packages apt-update install run)"
echo "debian: OK (hosted: build publish generated-index install run)"
