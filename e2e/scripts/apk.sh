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

# The job log is truncated well before the hosted section, so the state that
# decides which path is taken is printed FIRST. Nine rounds were spent guessing
# whether abuild was on PATH; the answer belongs at the top of the run.
echo "apk: runner = $(cat /etc/os-release 2>/dev/null | sed -n 's/^ID=//p') $(sed -n 's/^VERSION_ID=//p' /etc/os-release 2>/dev/null)"
echo "apk: shell  = $0 (bash: $(command -v bash || echo MISSING))"
for t in abuild abuild-keygen apk sha256sum tar; do
  echo "apk: $t = $(command -v "$t" || echo MISSING)"
done

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
# An APKBUILD is a SHELL SCRIPT, not a .ini file. `pkgname = x` with spaces is a
# command named "pkgname", which abuild reports as:
#   APKBUILD: line 1: pkgname: not found
# The assignments have no spaces around '='.
printf '%s\n' \
  "pkgname=$PKGNAME" \
  "pkgver=$VER" \
  "pkgrel=0" \
  'pkgdesc="easyblobs e2e"' \
  'url="https://example.com"' \
  'arch="x86_64"' \
  'license="Apache-2.0"' \
  'source=""' \
  '' \
  'package() {' \
  '  install -Dm755 "$startdir/ezhello" "$pkgdir/usr/bin/ezhello"' \
  '}' > "$SRC/APKBUILD"

APK=""
# abuild is the reference implementation and the job installs alpine-sdk, so use
# it. A hand-rolled .apk was tried first and never got past apk-tools:
#   archive does not contain expected data   (one tarball instead of segments)
#   BAD archive                              (still, with datahash added)
# That format has three concatenated segments and a signature chain; getting it
# right by hand is guesswork, and the point of this suite is the MIRROR.
echo "== hosted: which packager is available =="
for t in abuild abuild-keygen; do
  if command -v "$t" >/dev/null 2>&1; then
    echo "   $t: $(command -v "$t")"
  else
    echo "   $t: MISSING"
  fi
done
if command -v abuild >/dev/null 2>&1; then
  # abuild wants a source tarball named <pkgname>-<pkgver>.tar.gz in $SRCDEST,
  # and it refuses to run as root without -F.
  #
  # It also signs with a private key in ~/.abuild, so one is generated first:
  # without it abuild stops at "No private key found".
  if [ ! -f "$HOME/.abuild/abuild.conf" ]; then
    mkdir -p "$HOME/.abuild"
    # -n: no passphrase. NO -i: that flag tries to install the public key into
    # /etc/apk/keys with `doas`, and a GitHub container has neither:
    #   /usr/bin/abuild-keygen: line 63: doas: not found
    # The key is still written to ~/.abuild; it is copied into place below.
    abuild-keygen -a -n >/tmp/abuild-key.log 2>&1 || {
      echo "apk: abuild-keygen failed:" >&2
      tail -10 /tmp/abuild-key.log >&2
      exit 1; }
  fi
  # Install the public key where apk looks for it. We are root in this container,
  # so this is a plain copy - which is what -i would have done via doas.
  mkdir -p /etc/apk/keys
  for k in "$HOME"/.abuild/*.rsa.pub; do
    [ -f "$k" ] && cp "$k" /etc/apk/keys/ && echo "   installed key $(basename "$k")"
  done
  if [ ! -f "$HOME/.abuild/abuild.conf" ]; then
    {
      echo "PACKAGER=\"e2e <e2e@example.com>\""
      key=$(ls "$HOME"/.abuild/*.rsa 2>/dev/null | head -1)
      [ -n "$key" ] && echo "PACKAGER_PRIVKEY=\"$key\""
    } > "$HOME/.abuild/abuild.conf"
  fi
  echo "   abuild.conf:"; cat "$HOME/.abuild/abuild.conf" | sed 's/^/     /'
  SRCDEST="$WORK/srcdest"
  mkdir -p "$SRCDEST"
  ( cd "$SRC" && tar -czf "$SRCDEST/$PKGNAME-$VER.tar.gz" ezhello ) ||
    fail "tar the abuild source"
  ( cd "$SRC" && PKGDEST="$WORK/pkgs" abuild -F -r checksum >/tmp/abuild1.log 2>&1 ) || true
  ( cd "$SRC" && PKGDEST="$WORK/pkgs" abuild -F -r >/tmp/abuild.log 2>&1 ) || {
    echo "apk: abuild failed:" >&2
    tail -25 /tmp/abuild.log >&2
    exit 1; }
  APK=$(find "$WORK/pkgs" "$SRC" -name '*.apk' 2>/dev/null | head -1)
  echo "   abuild produced: ${APK:-<nothing>}"
  if [ -n "$APK" ]; then
    # A signed .apk is three concatenated gzipped tars. Say which segments it has
    # so a future BAD archive can be traced to the archive rather than guessed at.
    echo "   segments: $(gzip -l "$APK" 2>/dev/null | wc -l) gzip members hint"
    tar -tzf "$APK" 2>/dev/null | head -5 | sed 's/^/     /'
    # The public key must be where apk looks, or --allow-untrusted still fails the
    # signature check on a signed package.
    ls /etc/apk/keys/ 2>/dev/null | sed 's/^/     key: /'
  fi
fi
if [ -z "$APK" ]; then
  # No fallback. A hand-built .apk was attempted twice and never passed
  # apk-tools (first "archive does not contain expected data", then "BAD
  # archive" once datahash was added). Carrying that code means every future
  # failure has two possible causes and the log points at neither.
  echo "apk: abuild produced no .apk - the hosted case cannot run." >&2
  echo "apk: (alpine-sdk, which installs abuild, comes from the distro job)" >&2
  tail -30 /tmp/abuild.log >&2 2>/dev/null || true
  exit 1
fi
[ -s "$APK" ] || fail "no .apk produced"
echo "   archive: $APK ($(wc -c < "$APK") bytes, from abuild)"

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
  tail -10 /tmp/apk-hi.log >&2
  fail "hosted add"
fi

echo "apk: OK (pull: index update add run)"
echo "apk: OK (hosted: build publish generated-index add run)"
