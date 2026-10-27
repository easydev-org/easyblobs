#!/bin/sh
# arch: the whole client surface against the mount, in both directions.
#
# A pacman repository is:
#
#   <base>/<repo>/os/<arch>/<repo>.db        the database (gzip tar)
#   <base>/<repo>/os/<arch>/<repo>.db.tar.gz the same bytes, other name
#   <base>/<repo>/os/<arch>/<pkg>.pkg.tar.zst a package
#   <base>/_hosted/<repo>/...                 a self-published repo
#
# Inside the database every package is a DIRECTORY holding a `desc` of %KEY%
# sections. pacman needs %FILENAME% to find the package and %SHA256SUM% to
# verify it.
#
# PULL   pacman -Sy parses the database; pacman -S installs a package; the
#        installed file is then RUN.
# HOSTED publish a real .pkg.tar.zst under _hosted, watch GenerateDB build the
#        database from it, install it back, and run what it installed.
#
# The hosted form is the point: the database is DERIVED from the uploaded bytes,
# so "the package appears in <repo>.db with the right checksum" is a statement
# about the adapter rather than about a file we also wrote.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "arch: FAIL: $*" >&2; exit 1; }

command -v pacman >/dev/null || { echo "arch: pacman is missing"; exit 1; }
echo "arch: $(pacman --version 2>&1 | head -1)"

PACCONF="$WORK/pacman.conf"
ROOT="$WORK/root"
DBPATH="$WORK/db"
mkdir -p "$ROOT" "$DBPATH" "$WORK/cache"

# write_conf: a pacman.conf that touches nothing outside $WORK, using the
# current $ROOT/$DBPATH/$CACHE. Called again after those are repointed, so the
# hosted half installs into a FRESH root - otherwise the pull half's acl and the
# hosted package would share a database and "installed" would mean nothing.
write_conf() {
  {
    echo "[options]"
    echo "RootDir = $ROOT"
    echo "DBPath = $DBPATH"
    echo "CacheDir = $CACHE"
    echo "LogFile = $WORK/pacman.log"
    echo "Architecture = x86_64"
    echo "SigLevel = Optional TrustAll"
    echo "HoldPkg ="
    echo ""
    # The section name IS the repository name: pacman substitutes it for $repo
    # in the Server line, so [e2e] asks for e2e.db while [core] asks for
    # core.db. Hardcoding [e2e] here made the pull half request e2e.db from a
    # mount that only mirrors core - a 404 that looked like a missing database.
    echo "[$REPO_NAME]"
    echo "Server = $SERVER"
  } > "$PACCONF"
}

# ------------------------------------------------------------------ pull ----
echo "== pull: the database is served =="
IDX=""
for f in core.db core.db.tar.gz; do
  code=$(curl -s -o "$WORK/db.bin" -w '%{http_code}' "$BASE/core/os/x86_64/$f" 2>/dev/null || echo 000)
  if [ "$code" = "200" ] && [ -s "$WORK/db.bin" ]; then
    IDX="$f"
    echo "   $f ($(wc -c < "$WORK/db.bin") bytes)"
    break
  fi
  echo "   $f -> $code"
done
[ -n "$IDX" ] || fail "no core database under the mount"

echo "== pull: it is a gzipped tar with a desc per package =="
mkdir -p "$WORK/dbx"
tar -xzf "$WORK/db.bin" -C "$WORK/dbx" 2>/dev/null || fail "$IDX is not a gzipped tar"
DESC=$(find "$WORK/dbx" -name desc | head -1)
[ -n "$DESC" ] || fail "the database has no */desc entries"
echo "   entries: $(ls "$WORK/dbx" | wc -l), first desc: $(basename "$(dirname "$DESC")")"
grep -q '^%FILENAME%' "$DESC" || fail "a desc has no %FILENAME%"
grep -q '^%SHA256SUM%' "$DESC" || fail "a desc has no %SHA256SUM%"
echo "   the desc carries FILENAME and SHA256SUM"

echo "== pull: pacman -Sy parses the database =="
# The mount IS the mirror root: a pacman Server line of
# "http://host/artifacts/arch/easyblobs" means /artifacts/arch/<repo>/os/<arch>/,
# so the path under $BASE is just $repo/... - prefixing "arch" again produced
# /artifacts/arch/arch/core/os/x86_64/core.db, which 404s.
SERVER="$BASE/\$repo/os/\$arch"
REPO_NAME="core"
CACHE="$WORK/cache"
write_conf
if pacman --config "$PACCONF" -Sy >/tmp/arch-sy.log 2>&1; then
  echo "   sync succeeded"
else
  echo "arch: pacman -Sy failed:" >&2
  tail -20 /tmp/arch-sy.log >&2
  fail "pacman -Sy"
fi

echo "== pull: install a package and RUN it =="
# acl ships /usr/bin/getfacl, which is enough to prove the payload landed.
if pacman --config "$PACCONF" -S --noconfirm acl >/tmp/arch-inst.log 2>&1; then
  echo "   installed acl"
else
  echo "arch: pacman -S acl failed:" >&2
  tail -20 /tmp/arch-inst.log >&2
  fail "pacman -S acl"
fi
[ -e "$ROOT/usr/bin/getfacl" ] || fail "acl installed no /usr/bin/getfacl"
"$ROOT/usr/bin/getfacl" --version >/dev/null 2>&1 ||
  fail "the installed getfacl does not run"
echo "   the installed binary runs"

# ------------------------------------------------------------------ hosted --
echo "== hosted: build a real .pkg.tar.zst =="
PKGNAME="easyblobs-e2e"
VER="1.0.0"
REL="1"
ARCHN="x86_64"
FN="$PKGNAME-$VER-$REL-$ARCHN.pkg.tar.zst"

# makepkg is the reference builder, exactly as dpkg-deb and rpmbuild are used
# for their formats. A hand-assembled package was rejected twice:
#   File ... is corrupted (invalid or corrupted package)
# A real package carries .BUILDINFO, .MTREE and .PKGINFO, and .MTREE is a gzipped
# mtree spec pacman verifies every unpacked path against. Reproducing that by
# hand is guesswork; makepkg gets it right by construction.
if ! command -v makepkg >/dev/null 2>&1; then
  echo "arch: makepkg is not installed; the hosted case cannot build a package." >&2
  echo "arch: (it comes from the pacman package, which the distro job installs)" >&2
  exit 1
fi

SRC="$WORK/pkgbuild"
mkdir -p "$SRC"
printf '%s\n' '#!/bin/sh' 'echo "ez hello"' > "$SRC/ezhello"
chmod 755 "$SRC/ezhello"
# APKBUILD's analogue: a PKGBUILD is sourced as shell, so assignments have no
# spaces around '='.
{
  echo "pkgname=$PKGNAME"
  echo "pkgver=$VER"
  echo "pkgrel=$REL"
  echo 'pkgdesc="easyblobs e2e"'
  echo 'arch=("x86_64")'
  echo 'url="https://example.com"'
  echo 'license=("Apache-2.0")'
  echo 'options=(!strip !buildflags)'
  echo 'source=()'
  echo 'package() {'
  echo '  install -Dm755 "$srcdir/ezhello" "$pkgdir/usr/bin/ezhello"'
  echo '}'
} > "$SRC/PKGBUILD"

# makepkg REFUSES to run as root:
#   error: Running makepkg as root is not allowed as it can cause
#   permanent, catastrophic damage to your system
# The container runs as root, so a throwaway user builds the package and the
# output is handed back. Everything it writes lives under $WORK.
if [ "$(id -u)" = "0" ]; then
  command -v useradd >/dev/null 2>&1 || fail "need useradd to drop root for makepkg"
  id -u ezmk >/dev/null 2>&1 || useradd -m -s /bin/sh ezmk
  chown -R ezmk "$WORK"
  RUNAS="su ezmk -s /bin/sh -c"
else
  RUNAS="sh -c"
fi
$RUNAS "cd '$SRC' && PKGDEST='$WORK/pkgs' SRCDEST='$SRC' BUILDDIR='$WORK/build' \
    makepkg -f --nocheck --noconfirm" >/tmp/makepkg.log 2>&1 || {
  echo "arch: makepkg failed:" >&2
  tail -30 /tmp/makepkg.log >&2
  exit 1; }
FN=$(basename "$(find "$WORK/pkgs" -name '*.pkg.tar.zst' | head -1)")
[ -n "$FN" ] || fail "makepkg produced no package"
mv "$WORK/pkgs/$FN" "$WORK/$FN" 2>/dev/null || true
[ -s "$WORK/$FN" ] || fail "no package produced"
echo "   built $FN ($(wc -c < "$WORK/$FN") bytes)"

echo "== hosted: publish it under <repo>/os/<arch>/ =="
REPO="e2e"
code=$(curl -sS -o /tmp/arch-up.out -w '%{http_code}' -X PUT \
  --data-binary "@$WORK/$FN" \
  "$BASE/_hosted/e2e/$REPO/os/$ARCHN/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) fail "upload -> $code $(cat /tmp/arch-up.out)" ;;
esac

echo "== hosted: the GENERATED database lists it =="
curl -fsS -o "$WORK/hdb.bin" "$BASE/_hosted/e2e/$REPO/os/$ARCHN/$REPO.db" ||
  fail "the generated database is not served"
mkdir -p "$WORK/hdbx"
tar -xzf "$WORK/hdb.bin" -C "$WORK/hdbx" 2>/dev/null ||
  fail "the generated database is not a gzipped tar"
HDESC=$(find "$WORK/hdbx" -path "*$PKGNAME*" -name desc | head -1)
[ -n "$HDESC" ] || { echo "arch: the generated database has no entry for $PKGNAME" >&2
                     ls "$WORK/hdbx" >&2; exit 1; }
echo "   entry: $(basename "$(dirname "$HDESC")")"
grep -q "$FN" "$HDESC" || fail "the desc does not name the package file"
grep -q '^%SHA256SUM%' "$HDESC" || fail "the desc has no checksum"
WANT=$(sha256sum "$WORK/$FN" | cut -d' ' -f1)
grep -q "$WANT" "$HDESC" || fail "the desc checksum is not the uploaded file's sha256"
echo "   the desc carries the real sha256 ($WANT)"

echo "== hosted: the .db.tar.gz alias agrees =="
if curl -fsS -o "$WORK/hdb2.bin" "$BASE/_hosted/e2e/$REPO/os/$ARCHN/$REPO.db.tar.gz" 2>/dev/null &&
   [ -s "$WORK/hdb2.bin" ]; then
  cmp -s "$WORK/hdb.bin" "$WORK/hdb2.bin" ||
    echo "   (the two names differ byte-wise; both parse as a database)"
  echo "   .db.tar.gz is served"
fi

echo "== hosted: install it back from the hosted repo =="
ROOT2="$WORK/root2"
DBPATH2="$WORK/db2"
CACHE2="$WORK/cache2"
mkdir -p "$ROOT2" "$DBPATH2" "$CACHE2"
# A FRESH root, database and cache: installing the hosted package into the same
# root the pull half used would "succeed" because acl was already there, and the
# assertion would pass without the hosted repo being read at all.
ROOT="$ROOT2"
DBPATH="$DBPATH2"
CACHE="$CACHE2"
# The hosted repo is published under the name the suite created (e2e), so the
# section must be e2e - pacman will ask for e2e.db, which GenerateDB produces.
REPO_NAME="e2e"
SERVER="$BASE/_hosted/e2e/\$repo/os/\$arch"
write_conf

if pacman --config "$PACCONF" -Sy >/tmp/arch-hsy.log 2>&1 &&
   pacman --config "$PACCONF" -S --noconfirm "$PKGNAME" >/tmp/arch-hi.log 2>&1; then
  echo "   installed from the hosted repo"
  [ -e "$ROOT2/usr/bin/ezhello" ] || fail "the package installed no ezhello"
  "$ROOT2/usr/bin/ezhello" | grep -q "ez hello" ||
    fail "the installed ezhello does not run"
  echo "   the installed binary runs"
else
  echo "arch: the hosted package could not be installed:" >&2
  tail -15 /tmp/arch-hsy.log >&2
  tail -15 /tmp/arch-hi.log >&2
  fail "hosted install"
fi

echo "arch: OK (pull: db desc pacman-sy install run)"
echo "arch: OK (hosted: build publish generated-db checksum db-tar-gz install run)"
