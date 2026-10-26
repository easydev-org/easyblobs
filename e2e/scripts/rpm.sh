#!/bin/sh
# rpm: the whole client surface against the mount, in both directions.
#
# A dnf/yum repository is:
#
#   <base>/<repo>/repodata/repomd.xml        the metadata pointer
#   <base>/<repo>/repodata/*-primary.xml.gz  the package list
#   <base>/<repo>/Packages/<pkg>.rpm         the packages
#   <base>/_hosted/<repo>/...                a self-published repo
#
# PULL   dnf install resolves through repodata, fetches a .rpm and unpacks it;
#        the installed binary is then RUN.
# HOSTED publish a real .rpm under _hosted, watch GenerateRepodata describe it,
#        install it back from that repo, and run what it installed.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "rpm: FAIL: $*" >&2; exit 1; }

echo "== pull: point dnf at the mount =="
rm -f /etc/yum.repos.d/*.repo
printf '[main]\ninstall_weak_deps=False\nreleasever=44\n' > /etc/dnf/dnf.conf
printf '[fedora]\nname=fedora\nbaseurl=%s/pub/fedora/linux/releases/43/Everything/x86_64/os/\nenabled=1\ngpgcheck=0\noptional_metadata_types=primary\n' \
  "$BASE" > /etc/yum.repos.d/fedora.repo

echo "== pull: repomd.xml is served =="
RM=""
for f in pub/fedora/linux/releases/43/Everything/x86_64/os/repodata/repomd.xml \
         repodata/repomd.xml; do
  if curl -fsS -o "$WORK/rm" "$BASE/$f" 2>/dev/null && [ -s "$WORK/rm" ]; then
    RM="$f"
    echo "   $f ($(wc -c < "$WORK/rm") bytes)"
    break
  fi
done
[ -n "$RM" ] || fail "no repomd.xml in the repository"

echo "== pull: install and RUN a package =="
dnf -y --refresh install jq >/tmp/dnf-install.log 2>&1 || {
  echo "rpm: dnf install jq failed:" >&2
  tail -20 /tmp/dnf-install.log >&2
  fail "dnf install"; }
rpm -q jq >/dev/null || fail "jq is not recorded as installed"
echo '{"ok":true}' | jq -e '.ok' >/dev/null || fail "the installed jq does not work"
echo "   jq runs: $(jq --version)"

# ------------------------------------------------------------------ hosted --
echo "== hosted: build a real .rpm =="
PKGNAME="easyblobs-e2e"
VER="1.0.0"
if ! command -v rpmbuild >/dev/null 2>&1; then
  echo "   (rpmbuild is not present; the hosted upload still gets exercised)"
fi

TOPDIR="$WORK/rpmbuild"
mkdir -p "$TOPDIR/SPECS" "$TOPDIR/SOURCES" "$TOPDIR/BUILD" \
         "$TOPDIR/RPMS" "$TOPDIR/SRPMS"
printf '%s\n' '#!/bin/sh' 'echo "ez hello"' > "$TOPDIR/SOURCES/ezhello"
chmod 755 "$TOPDIR/SOURCES/ezhello"
{
  echo "Name: $PKGNAME"
  echo "Version: $VER"
  echo "Release: 1"
  echo "Summary: easyblobs e2e"
  echo "License: Apache-2.0"
  echo "BuildArch: x86_64"
  echo ""
  echo "%description"
  echo "e2e"
  echo ""
  echo "%install"
  echo "mkdir -p %{buildroot}/usr/bin"
  echo "install -m755 %{_sourcedir}/ezhello %{buildroot}/usr/bin/ezhello"
  echo ""
  echo "%files"
  echo "/usr/bin/ezhello"
} > "$TOPDIR/SPECS/$PKGNAME.spec"

RPMFILE=""
if command -v rpmbuild >/dev/null 2>&1; then
  rpmbuild --define "_topdir $TOPDIR" -bb "$TOPDIR/SPECS/$PKGNAME.spec" \
    >/tmp/rpmbuild.log 2>&1 || {
      echo "rpm: rpmbuild failed:" >&2
      tail -15 /tmp/rpmbuild.log >&2
      exit 1; }
  RPMFILE=$(find "$TOPDIR/RPMS" -name '*.rpm' 2>/dev/null | head -1)
  [ -n "$RPMFILE" ] || fail "rpmbuild produced no .rpm"
else
  fail "rpmbuild is required to build a real .rpm for the hosted case"
fi

echo "== hosted: publish under _hosted/<repo>/ =="
FN=$(basename "$RPMFILE")
code=$(curl -sS -o /tmp/rpm-up.out -w '%{http_code}' -X PUT \
  --data-binary "@$RPMFILE" "$BASE/_hosted/e2e/Packages/$FN")
case "$code" in 200|201) echo "   uploaded (HTTP $code)" ;;
  *) fail "upload -> $code $(cat /tmp/rpm-up.out)" ;; esac

echo "== hosted: the generated repodata lists it =="
if curl -fsS -o "$WORK/hrepomd" "$BASE/_hosted/e2e/repodata/repomd.xml" 2>/dev/null &&
   [ -s "$WORK/hrepomd" ]; then
  echo "   repomd.xml is generated"
  grep -qi "primary" "$WORK/hrepomd" ||
    fail "the generated repomd.xml has no primary metadata"
  # The primary metadata is where the package name lives.
  PRIM=$(grep -oE '[^<>"]*-primary\.xml(\.gz)?' "$WORK/hrepomd" | head -1)
  if [ -n "$PRIM" ]; then
    if curl -fsS -o "$WORK/prim" "$BASE/_hosted/e2e/repodata/$PRIM" 2>/dev/null; then
      case "$PRIM" in
        *.gz) gunzip -c "$WORK/prim" > "$WORK/prim.xml" 2>/dev/null || true ;;
        *) cp "$WORK/prim" "$WORK/prim.xml" ;;
      esac
      if grep -q "$PKGNAME" "$WORK/prim.xml" 2>/dev/null; then
        echo "   the primary metadata lists $PKGNAME"
      else
        fail "the generated primary metadata does not list the uploaded package"
      fi
    fi
  fi
else
  echo "   (no generated repodata at the repo root; the install below decides)"
fi

echo "== hosted: install it back from the hosted repo =="
printf '[hosted]\nname=hosted\nbaseurl=%s/_hosted/e2e\nenabled=1\ngpgcheck=0\n' \
  "$BASE" > /etc/yum.repos.d/hosted.repo
rm -f /etc/yum.repos.d/fedora.repo
if dnf -y --refresh install "$PKGNAME" >/tmp/dnf-h.log 2>&1; then
  echo "   installed from the hosted repo"
  command -v ezhello >/dev/null || fail "the package installed no ezhello"
  ezhello | grep -q "ez hello" || fail "the installed ezhello does not run"
  echo "   the installed binary runs"
else
  echo "rpm: the hosted package could not be installed:" >&2
  tail -20 /tmp/dnf-h.log >&2
  fail "hosted install"
fi

echo "rpm: OK (pull: repomd dnf-install rpm-q run)"
echo "rpm: OK (hosted: build publish generated-repodata install run)"
