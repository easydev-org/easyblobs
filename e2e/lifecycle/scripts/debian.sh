#!/bin/sh
# debian lifecycle: build+PUT a .deb into the HOSTED repo
# /artifacts/debian/_hosted/<repo>/ , then apt-get install it from that repo
# (public+private+upgrade), and delete via DELETE.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HOME="/tmp/lc-home-${STAGE}"

REPO=easy-platform
HOSTED="$EASYBLOBS_BASE/_hosted/$REPO"
PKG="lc-probe-${SUFFIX}"
PKGDIR="lc-probe-${SUFFIX}"
ARCH=amd64
SRC=/etc/apt/sources.list.d/lc.list

pick_arch() {
  # APT_ARCH keeps the toolchain-native arch.
  ARCH="$(dpkg --print-architecture)"
}

build_deb() { # $1 = version
  rm -rf "$WORK/pkg"
  mkdir -p "$WORK/pkg/DEBIAN" "$WORK/pkg/usr/bin"
  cd "$WORK/pkg"
  cat > "DEBIAN/control" <<X
Package: ${PKGDIR}
Version: $1
Architecture: ${ARCH}
Maintainer: lc <lc@easy-platform.invalid>
Description: lc probe package ${SUFFIX}
Section: misc
Priority: optional
X
  printf '#!/bin/sh\necho "lc-probe %s"\n' "$1" > "$PKGDIR"
  mv "$PKGDIR" usr/bin/
  chmod 0755 "usr/bin/${PKGDIR}"
  dpkg-deb --build --root-owner-group . "$WORK/${PKGDIR}_$1_${ARCH}.deb" >/dev/null
}

publish_deb() { # $1 = version
  build_deb "$1"
  # Upload to the hosted repo path (self-published, never probed as a proxy).
  code="$(curl -s -o /dev/null -w '%{http_code}' -X PUT \
    --data-binary "@$WORK/${PKGDIR}_$1_${ARCH}.deb" \
    "$HOSTED/${PKGDIR}_$1_${ARCH}.deb")"
  [ "$code" = "201" ] || [ "$code" = "200" ] || { echo "debian: upload rc=$code"; exit 1; }
}

configure_repo() {
  printf 'deb [trusted=yes] %s ./\n' "$HOSTED" > "$SRC"
}

install_pkg() { # $1 = expected version
  export PATH="/usr/local/bin:/usr/bin:/bin:$PATH"
  apt-get -o Acquire::Retries=0 \
    -o Dir::Etc::sourcelist="$SRC" -o Dir::Etc::sourceparts="-" -o APT::Get::List-Cleanup="0" \
    update >/dev/null 2>&1
  apt-get -o Acquire::Retries=0 \
    -o Dir::Etc::sourcelist="$SRC" -o Dir::Etc::sourceparts="-" \
    install -y --allow-unauthenticated --reinstall "${PKGDIR}" >/dev/null 2>&1
  out="$("$PKGDIR" 2>&1 | tail -1)"
  echo "$out" | grep -q "$1" || { echo "debian: got '$out' want $1"; return 1; }
  echo "debian: ${PKGDIR} $1 ok"
}

case "$STAGE" in
publish)
  pick_arch
  publish_deb "$V1"
  echo "debian: published ${PKGDIR}@$V1"
  ;;
public)
  # Pull-through: install a stock package from the debian mirror.
  mkdir -p /etc/apt/sources.list.d
  rm -f /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list
  printf 'deb [trusted=yes] %s/debian bookworm main\n' "$EASYBLOBS_BASE" > /etc/apt/sources.list.d/artifact.list
  apt-get update -o Acquire::Retries=0 >/dev/null 2>&1
  apt-get install -y --no-install-recommends jq >/dev/null 2>&1
  echo '{"ok":true}' | jq -e '.ok' >/dev/null
  echo "debian: public jq $(jq --version) installed"
  ;;
private)
  configure_repo
  install_pkg "$V1"
  ;;
upgrade)
  pick_arch
  publish_deb "$V2"
  configure_repo
  install_pkg "$V2"
  ;;
delete)
  pick_arch
  code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE \
    "$HOSTED/${PKGDIR}_${V2}_${ARCH}.deb")"
  [ "$code" = "204" ] || [ "$code" = "200" ] || { echo "debian: delete rc=$code"; exit 1; }
  idx="$(curl -s "$HOSTED/Packages")"
  echo "$idx" | grep -q "$V2" && { echo "debian: $V2 still indexed"; exit 1; }
  echo "debian: deleted ${PKGDIR}@$V2"
  ;;
esac
