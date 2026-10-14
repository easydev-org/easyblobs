#!/bin/sh
# apk lifecycle: abuild a real package + PUT it into the HOSTED repo
# /artifacts/apk/_hosted/<repo>/<arch>/ , then `apk add` it
# (public+private+upgrade) and delete via DELETE.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HOME="/tmp/lc-home-${STAGE}"

REPO=easy-platform
HOSTED="$EASYBLOBS_BASE/_hosted/$REPO"
ARCH=x86_64
PKG="lc-probe-${SUFFIX}"
CDN="$EASYBLOBS_BASE"
CURL="curl -sS"

repo_url() { echo "${CDN}/${REPO}"; }

ensure_tools() {
  # abuild (builds a real signed v2 package) and build-base come from the
  # normal Alpine mirror (pull-through); the hosted repo is also listed so a
  # previously-published world entry resolves.
  printf '%s\n%s\n%s\n' "${CDN}/v3.24/main" "${CDN}/v3.24/community" "$HOSTED" > /etc/apk/repositories
  apk add --allow-untrusted --no-cache abuild build-base >/dev/null 2>&1
  if [ ! -f "$HOME/.abuild/abuild.conf" ]; then
    abuild-keygen -a -n >/dev/null 2>&1
  fi
}

build_pkg() { # $1 = version
  rm -rf "$WORK/pkg" "$WORK/artifacts"
  mkdir -p "$WORK/pkg"
  cd "$WORK/pkg"
  cat > APKBUILD <<X
pkgname=${PKG}
pkgver=$1
pkgrel=0
pkgdesc="lc probe package ${SUFFIX}"
url="https://easy-platform.invalid/lc"
arch="${ARCH}"
license="MIT"
package() {
  mkdir -p "\$pkgdir/usr/bin"
  printf '#!/bin/sh\necho "lc-probe %s"\n' "\$pkgver" > "\$pkgdir/usr/bin/${PKG}"
  chmod 0755 "\$pkgdir/usr/bin/${PKG}"
}
X
  abuild -F -P "$WORK/artifacts" >/dev/null 2>&1 || true
  find "$WORK/artifacts" -name "${PKG}-$1-r0.apk" | grep -q . || { echo "apk: abuild failed"; exit 1; }
}

publish_pkg() { # $1 = version
  build_pkg "$1"
  apk="$WORK/artifacts/${PKG}/${ARCH}/${PKG}-$1-r0.apk"
  [ -f "$apk" ] || apk="$(find "$WORK/artifacts" -name "${PKG}-$1-r0.apk" | head -1)"
  [ -f "$apk" ] || { echo "apk: built package not found"; exit 1; }
  code="$($CURL -o /dev/null -w '%{http_code}' -X PUT --data-binary "@$apk" \
    "$HOSTED/${ARCH}/${PKG}-$1-r0.apk")"
  [ "$code" = "201" ] || [ "$code" = "200" ] || { echo "apk: upload rc=$code"; exit 1; }
}

install_pkg() { # $1 = expected version
  printf '%s\n%s\n%s\n' "${CDN}/v3.24/main" "${CDN}/v3.24/community" "$HOSTED" > /etc/apk/repositories
  apk update --allow-untrusted >/dev/null 2>&1
  apk add --allow-untrusted --upgrade "${PKG}" >/dev/null 2>&1
  out="$("${PKG}" 2>&1 | tail -1)"
  echo "$out" | grep -q "$1" || { echo "apk: got '$out' want $1"; return 1; }
  echo "apk: ${PKG} $1 ok"
}

ensure_tools

case "$STAGE" in
publish)
  publish_pkg "$V1"
  echo "apk: published ${PKG}@$V1"
  ;;
public)
  printf '%s\n%s\n' "${CDN}/v3.24/main" "${CDN}/v3.24/community" > /etc/apk/repositories
  apk update --allow-untrusted >/dev/null 2>&1
  apk add --allow-untrusted --no-cache jq >/dev/null 2>&1
  echo '{"ok":true}' | jq -e '.ok' >/dev/null
  echo "apk: public jq $(jq --version) installed"
  ;;
private)
  install_pkg "$V1"
  ;;
upgrade)
  publish_pkg "$V2"
  install_pkg "$V2"
  ;;
delete)
  code="$($CURL -o /dev/null -w '%{http_code}' -X DELETE \
    "$HOSTED/${ARCH}/${PKG}-${V2}-r0.apk")"
  [ "$code" = "204" ] || [ "$code" = "200" ] || { echo "apk: delete rc=$code"; exit 1; }
  idx="$($CURL "$HOSTED/${ARCH}/APKINDEX.tar.gz" | tar -xzO APKINDEX)"
  echo "$idx" | grep -q "V:${V2}-r0" && { echo "apk: $V2 still indexed"; exit 1; }
  echo "apk: deleted ${PKG}@$V2"
  ;;
esac
