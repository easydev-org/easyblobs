#!/bin/sh
# cran: the whole client surface against the mirror, in both directions.
#
# A CRAN repository is a plain HTTP tree:
#
#   <base>/src/contrib/PACKAGES          the package index
#   <base>/src/contrib/PACKAGES.gz       the same, gzipped
#   <base>/src/contrib/<pkg>_<ver>.tar.gz   a source package
#
# PULL   install.packages parses PACKAGES, fetches the tarball, unpacks and
#        installs it; the package is then LOADED and used.
# HOSTED the tree has a Generator (GenerateTreeIndex -> cranPACKAGES), so a
#        source tarball published under _hosted/<repo>/src/contrib/ makes R's
#        own PACKAGES appear - that index is synthesised from the upload, so
#        "it lists my package" is a statement about the adapter.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "cran: FAIL: $*" >&2; exit 1; }

command -v Rscript >/dev/null || { echo "cran: Rscript is missing"; exit 1; }
echo "cran: $(Rscript --version 2>&1 | head -1)"

echo "== pull: the index is served =="
IDX=""
for f in src/contrib/PACKAGES src/contrib/PACKAGES.gz; do
  code=$(curl -s -o "$WORK/idx" -w '%{http_code}' "$BASE/$f" 2>/dev/null || echo 000)
  if [ "$code" = "200" ] && [ -s "$WORK/idx" ]; then
    IDX="$f"
    echo "   $f ($(wc -c < "$WORK/idx") bytes)"
    break
  fi
  echo "   $f -> $code"
done
[ -n "$IDX" ] || fail "no PACKAGES index under the mount"

echo "== pull: install a package and USE it =="
LIBS="$WORK/lib"
mkdir -p "$LIBS"
# NOT quiet, and the assertion is the installed DIRECTORY, not the exit status.
# This is the same trap the hosted half already fell into: install.packages
# returns 0 while installing nothing, and quiet=TRUE means the only symptom is
# the next line.
if Rscript -e "install.packages('jsonlite', lib='$LIBS', repos='$BASE', dependencies=FALSE)" \
     >/tmp/cran-install.log 2>&1; then
  if [ ! -d "$LIBS/jsonlite" ]; then
    echo "cran: install.packages returned 0 but installed nothing:" >&2
    tail -25 /tmp/cran-install.log >&2
    fail "jsonlite was not installed"
  fi
  echo "   installed"
else
  echo "cran: install.packages failed:" >&2
  tail -25 /tmp/cran-install.log >&2
  fail "install.packages"
fi

# Loading is not the same as working: call it.
OUT=$(Rscript -e ".libPaths('$LIBS'); library(jsonlite); cat(toJSON(list(ok=TRUE), auto_unbox=TRUE))" 2>&1) ||
  { echo "cran: the installed package does not load:" >&2; echo "$OUT" >&2; exit 1; }
echo "$OUT" | grep -q '"ok":true' || fail "jsonlite did not serialise"
echo "   the installed package runs: $OUT"

# ------------------------------------------------------------------ hosted --
echo "== hosted: build a source package =="
PKG="easyblobsE2e"
VER="1.0.0"
SRC="$WORK/$PKG"
mkdir -p "$SRC/R"
{
  echo "Package: $PKG"
  echo "Type: Package"
  echo "Title: easyblobs e2e"
  echo "Version: $VER"
  echo "Author: e2e"
  echo "Maintainer: e2e <e2e@example.com>"
  echo "Description: e2e"
  echo "License: Apache-2.0"
  echo "Encoding: UTF-8"
} > "$SRC/DESCRIPTION"
printf '%s\n' 'greet <- function() "ez hello"' > "$SRC/R/greet.R"
# R refuses to install a source package without a NAMESPACE file:
#   ERROR: a 'NAMESPACE' file is required
# It is what declares the exports; a package with none is not a package.
printf '%s\n' 'export(greet)' > "$SRC/NAMESPACE"

TGZ="$PKG"_"$VER".tar.gz
( cd "$WORK" && tar -czf "$TGZ" "$PKG" ) || fail "build the source package"
[ -s "$WORK/$TGZ" ] || fail "no source package produced"

echo "== hosted: publish it under src/contrib/ =="
code=$(curl -sS -o /tmp/cran-up.out -w '%{http_code}' -X PUT \
  --data-binary "@$WORK/$TGZ" "$BASE/_hosted/e2e/src/contrib/$TGZ")
case "$code" in
  200|201) echo "   PUT $TGZ -> $code" ;;
  *) fail "upload -> $code $(cat /tmp/cran-up.out)" ;;
esac

echo "== hosted: the GENERATED index lists it =="
# cranPACKAGES builds this from the uploaded tarball names; it is not a file we
# wrote, so its contents are evidence about the adapter.
curl -fsS -o "$WORK/hpkgs" "$BASE/_hosted/e2e/src/contrib/PACKAGES" ||
  fail "the generated PACKAGES is not served"
grep -q "Package: $PKG" "$WORK/hpkgs" ||
  { echo "cran: the generated PACKAGES does not list $PKG:" >&2; head -10 "$WORK/hpkgs" >&2; exit 1; }
grep -q "Version: $VER" "$WORK/hpkgs" || fail "the generated PACKAGES has the wrong version"
echo "   PACKAGES lists $PKG $VER"

echo "== hosted: the gzipped index agrees =="
if curl -fsS "$BASE/_hosted/e2e/src/contrib/PACKAGES.gz" -o "$WORK/hp.gz" 2>/dev/null &&
   [ -s "$WORK/hp.gz" ]; then
  gunzip -c "$WORK/hp.gz" | grep -q "Package: $PKG" ||
    fail "PACKAGES.gz disagrees with PACKAGES"
  echo "   PACKAGES.gz agrees"
fi

echo "== hosted: install it back from the hosted repo =="
LIBS2="$WORK/lib2"
mkdir -p "$LIBS2"
# NOT quiet: install.packages returning 0 while installing nothing is exactly
# what happened the first time round - "installed from the hosted repo" printed,
# then library() said 'there is no package called'. A quiet install that fails
# silently is worse than a loud one that fails.
if Rscript -e "install.packages('$PKG', lib='$LIBS2', repos='$BASE/_hosted/e2e', dependencies=FALSE)" \
     >/tmp/cran-h.log 2>&1; then
  # The exit status is not the assertion - the installed directory is.
  if [ ! -d "$LIBS2/$PKG" ]; then
    echo "cran: install.packages succeeded but installed nothing." >&2
    echo "cran: its log said:" >&2
    tail -25 /tmp/cran-h.log >&2
    fail "the hosted package was not installed"
  fi
  echo "   installed from the hosted repo"
  OUT2=$(Rscript -e ".libPaths('$LIBS2'); library($PKG); cat(greet())" 2>&1) ||
    { echo "cran: the published package does not load:" >&2; echo "$OUT2" >&2; exit 1; }
  echo "$OUT2" | grep -q "ez hello" || fail "the published package's function does not run"
  echo "   its function runs: $OUT2"
else
  echo "cran: installing from the hosted repo failed:" >&2
  tail -25 /tmp/cran-h.log >&2
  fail "hosted install"
fi

echo "cran: OK (pull: packages-index install load)"
echo "cran: OK (hosted: build publish generated-index packages-gz install run)"
