#!/bin/sh
# swift lifecycle: publish a source archive (raw PUT) / resolve public+private /
# upgrade / delete (DELETE on the release path). The registry is the artifact
# mount (plain HTTP).
#
# Notes (Swift 6.4):
# - The registry URL is plain HTTP, so `swift package-registry set` needs
#   `--allow-insecure-http`, or SPM rejects the non-HTTPS URL.
# - `swift package-registry publish` refuses to publish to a plain-HTTP registry
#   even with that flag (that is the artifact hosted face), so publish is a raw
#   PUT of the source archive: PUT <base>/<scope>/<name>/<version>.
#   `--dry-run` is useless here: it reports success before any network call.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HOME="/tmp/lc-home-${STAGE}"
# SwiftPM's security fingerprint DB pins the first checksum it saw for a
# package; start each run clean so a re-published version is not rejected.
rm -rf /root/.swiftpm/security /root/.cache/org.swift.swiftpm/registries
mkdir -p /root/.swiftpm/configuration
# Plain HTTP registry: SPM 6.x needs --allow-insecure-http (fall back for older
# SPM that lacks the flag).
swift package-registry set --global --allow-insecure-http "$EASYBLOBS_BASE" >/dev/null 2>&1 \
  || swift package-registry set --global "$EASYBLOBS_BASE" >/dev/null 2>&1

make_pkg() { # $1 = version
  rm -rf "$WORK/pkg"
  mkdir -p "$WORK/pkg/Sources/LcProbe"
  cd "$WORK/pkg"
  cat > Package.swift <<X
// swift-tools-version:5.9
import PackageDescription
let package = Package(
  name: "LcProbe",
  products: [.library(name: "LcProbe", targets: ["LcProbe"])],
  targets: [.target(name: "LcProbe")]
)
X
  cat > Sources/LcProbe/LcProbe.swift <<X
public enum LcProbe {
  public static let version = "$1"
}
X
}

publish_pkg() { # $1 = version
  make_pkg "$1"
  zip="$WORK/lc-probe-$1.zip"
  rm -f "$zip"
  # The registry publish payload is the SPM source archive.
  ( cd "$WORK/pkg" && swift package archive-source --output "$zip" >/dev/null 2>&1 ) \
    || { cd "$WORK/pkg" && swift package archive-source >/dev/null 2>&1 && cp "$WORK/pkg/LcProbe.zip" "$zip"; }
  [ -s "$zip" ] || { echo "swift: no source archive produced"; exit 1; }
  # Raw PUT (scope=lc, name=probe). A token is only needed on a closed instance.
  if [ -n "${EASYBLOBS_TOKEN:-}" ]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' -X PUT \
      -H "authorization: Bearer $EASYBLOBS_TOKEN" --data-binary "@$zip" \
      "$EASYBLOBS_BASE/lc/probe/$1")"
  else
    code="$(curl -s -o /dev/null -w '%{http_code}' -X PUT --data-binary "@$zip" \
      "$EASYBLOBS_BASE/lc/probe/$1")"
  fi
  [ "$code" = "201" ] || [ "$code" = "200" ] || { echo "swift: publish rc=$code"; exit 1; }
}

use_app() { # $1 = version constraint, $2 = expected
  rm -rf "$WORK/use"
  mkdir -p "$WORK/use/Sources/Use"
  cd "$WORK/use"
  cat > Package.swift <<X
// swift-tools-version:5.9
import PackageDescription
let package = Package(
  name: "Use",
  dependencies: [.package(id: "lc.probe", from: "$1")],
  targets: [.executableTarget(name: "Use", dependencies: [.product(name: "LcProbe", package: "lc.probe")])]
)
X
  cat > Sources/Use/main.swift <<X
import LcProbe
if LcProbe.version != "$2" { fatalError("bad version " + LcProbe.version) }
print("swift: lc.probe " + LcProbe.version)
X
  swift package resolve >/dev/null 2>&1
  swift run 2>&1 | tail -1
}

case "$STAGE" in
publish)
  publish_pkg "$V1"
  echo "swift: published lc.probe@$V1"
  ;;
public)
  # SwiftPM has no public registry upstream; assert the registry serves an
  # anonymous listing.
  code="$(curl -s -o /dev/null -w '%{http_code}' "$EASYBLOBS_BASE/lc/probe")"
  [ "$code" = "200" ] || { echo "swift: releases rc=$code"; exit 1; }
  echo "swift: registry reachable (no public upstream)"
  ;;
private)
  use_app "$V1" "$V1"
  ;;
upgrade)
  publish_pkg "$V2"
  use_app "$V2" "$V2"
  ;;
delete)
  code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$EASYBLOBS_BASE/lc/probe/$V2")"
  [ "$code" = "200" ] || [ "$code" = "204" ] || { echo "swift: delete rc=$code"; exit 1; }
  rm -rf "$WORK/use" /root/.swiftpm/security
  rel="$(curl -s "$EASYBLOBS_BASE/lc/probe")"
  echo "$rel" | grep -q ""$V2"" && { echo "swift: $V2 still listed after delete"; exit 1; }
  echo "$rel" | grep -q ""$V1"" || { echo "swift: $V1 vanished too"; exit 1; }
  echo "swift: deleted lc.probe@$V2 ($V1 survives)"
  ;;
esac
