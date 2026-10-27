#!/bin/sh
# apk: the whole client surface against the mount, in both directions.
#
# An Alpine repository is:
#
#   <base>/<repo>/<arch>/APKINDEX.tar.gz    the signed index
#   <base>/<repo>/<arch>/<pkg>.apk          the packages
#   <base>/_hosted/<repo>/...               a self-published repo
#
# PULL   apk update parses APKINDEX; apk add fetches a .apk and unpacks it; the
#        installed binary is then RUN.
# HOSTED publish a real .apk under _hosted, watch GenerateAPKINDEX list it, add
#        it back from that repo, and run what it installed.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "apk: FAIL: $*" >&2; exit 1; }

echo "== pull: point apk at the mount =="
printf '%s/v3.24/main\n%s/v3.24/community\n' "$BASE" "$BASE" > /etc/apk/repositories

echo "== pull: the index is served =="
IDX=""
for f in v3.24/main/x86_64/APKINDEX.tar.gz v3.24/main/x86_64/APKINDEX; do
  if curl -fsS -o "$WORK/idx" "$BASE/$f" 2>/dev/null && [ -s "$WORK/idx" ]; then
    IDX="$f"
    echo "   $f ($(wc -c < "$WORK/idx") bytes)"
    break
  fi
done
[ -n "$IDX" ] || fail "no APKINDEX in the repository"

echo "== pull: apk update =="
apk update >/tmp/apk-update.log 2>&1 || {
  echo "apk: apk update failed:" >&2
  tail -15 /tmp/apk-update.log >&2
  fail "apk update"; }
echo "   update succeeded"

echo "== pull: add and RUN a package =="
# --allow-untrusted: the mirror serves the upstream bytes but this suite carries
# no Alpine signing keys.
apk add --no-cache --allow-untrusted jq >/tmp/apk-add.log 2>&1 || {
  echo "apk: apk add jq failed:" >&2
  tail -15 /tmp/apk-add.log >&2
  fail "apk add"; }
echo '{"ok":true}' | jq -e '.ok' >/dev/null || fail "the installed jq does not work"
echo "   jq runs: $(jq --version)"

# ------------------------------------------------------------------ hosted --
echo "== hosted: build a real .apk =="
PKGNAME="easyblobs-e2e"
VER="1.0.0"
SRC="$WORK/apksrc"
mkdir -p "$SRC"
printf '%s\n' '#!/bin/sh' 'echo "ez hello"' > "$SRC/ezhello"
chmod 755 "$SRC/ezhello"
printf '%s\n' \
  "pkgname = $PKGNAME" \
  "pkgver = $VER-r0" \
  'pkgdesc = easyblobs e2e' \
  'arch = x86_64' \
  'license = Apache-2.0' \
  'package() {' \
  '  install -Dm755 "$srcdir/ezhello" "$pkgdir/usr/bin/ezhello"' \
  '}' > "$SRC/APKBUILD"

APK=""
if command -v abuild >/dev/null 2>&1; then
  ( cd "$SRC" && abuild -F checksum >/dev/null 2>&1 || true;
    cd "$SRC" && abuild -F >/tmp/abuild.log 2>&1 ) || true
  APK=$(find "$SRC" -name '*.apk' 2>/dev/null | head -1)
fi
if [ -z "$APK" ]; then
  # A .apk is NOT one tarball. It is up to three CONCATENATED gzipped tars:
  #
  #   [signature segment]  .SIGN.RSA.*       (optional with --allow-untrusted)
  #    control segment     .PKGINFO (+ scripts)
  #    data segment        the payload
  #
  # A single tar with .PKGINFO and the payload together is rejected by apk:
  #   ERROR: <pkg>: archive does not contain expected data
  # So the two segments are built separately and cat'ed.
  CTRL="$WORK/apkctrl"
  DATA="$WORK/apkdata"
  mkdir -p "$CTRL" "$DATA/usr/bin"
  cp "$SRC/ezhello" "$DATA/usr/bin/ezhello"
  chmod 755 "$DATA/usr/bin/ezhello"
  # The data segment has to be built FIRST: .PKGINFO must carry datahash, the
  # sha256 of that segment. Without it apk says:
  #   WARNING: Support for packages without datahash will be dropped in apk-tools 3.
  #   ERROR: <pkg>: BAD archive
  ( cd "$DATA" && tar -czf "$WORK/data.tar.gz" usr ) || fail "data segment"
  DATAHASH=$(sha256sum "$WORK/data.tar.gz" | cut -d' ' -f1)
  {
    echo "pkgname = $PKGNAME"
    echo "pkgver = $VER-r0"
    echo "pkgdesc = easyblobs e2e"
    echo "url = https://example.com"
    echo "arch = x86_64"
    echo "size = $(wc -c < "$SRC/ezhello")"
    echo "origin = $PKGNAME"
    echo "commit = e2e"
    echo "license = Apache-2.0"
    echo "datahash = $DATAHASH"
  } > "$CTRL/.PKGINFO"
  ( cd "$CTRL" && tar -czf "$WORK/ctrl.tar.gz" .PKGINFO ) || fail "control segment"
  cat "$WORK/ctrl.tar.gz" "$WORK/data.tar.gz" > "$WORK/$PKGNAME-$VER-r0.apk" ||
    fail "concatenate the .apk segments"
  APK="$WORK/$PKGNAME-$VER-r0.apk"
fi
[ -s "$APK" ] || fail "no .apk produced"

echo "== hosted: publish under _hosted/<repo>/ =="
FN=$(basename "$APK")
code=$(curl -sS -o /tmp/apk-up.out -w '%{http_code}' -X PUT \
  --data-binary "@$APK" "$BASE/_hosted/e2e/x86_64/$FN")
case "$code" in 200|201) echo "   uploaded (HTTP $code)" ;;
  *) fail "upload -> $code $(cat /tmp/apk-up.out)" ;; esac

echo "== hosted: the generated index lists it =="
if curl -fsS -o "$WORK/hidx.tar.gz" "$BASE/_hosted/e2e/x86_64/APKINDEX.tar.gz" 2>/dev/null &&
   [ -s "$WORK/hidx.tar.gz" ]; then
  mkdir -p "$WORK/hidx"
  tar -xzf "$WORK/hidx.tar.gz" -C "$WORK/hidx" 2>/dev/null || true
  if grep -q "$PKGNAME" "$WORK/hidx/APKINDEX" 2>/dev/null; then
    echo "   the generated APKINDEX lists $PKGNAME"
  else
    fail "the generated APKINDEX does not list the uploaded package"
  fi
else
  echo "   (no generated APKINDEX at the repo root; the add below decides)"
fi

echo "== hosted: add it back from the hosted repo =="
printf '%s/_hosted/e2e\n' "$BASE" > /etc/apk/repositories
if apk update --allow-untrusted >/tmp/apk-h.log 2>&1 &&
   apk add --allow-untrusted "$PKGNAME" >/tmp/apk-hi.log 2>&1; then
  echo "   installed from the hosted repo"
  command -v ezhello >/dev/null || fail "the package installed no ezhello"
  ezhello | grep -q "ez hello" || fail "the installed ezhello does not run"
  echo "   the installed binary runs"
else
  echo "apk: the hosted package could not be added:" >&2
  tail -15 /tmp/apk-h.log >&2
  tail -15 /tmp/apk-hi.log >&2
  fail "hosted add"
fi

echo "apk: OK (pull: index update add run)"
echo "apk: OK (hosted: build publish generated-index add run)"
