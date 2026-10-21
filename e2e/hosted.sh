#!/bin/sh
# hosted.sh <protocol> — HOSTED publish + fetch through the real HTTP surface.
#
# Unlike scripts/*.sh (which drive a REAL package manager against a PUBLIC
# upstream and therefore need outbound network), this drives the HOSTED path:
# it PUTs bytes into easyblobs and GETs them back. It needs nothing but the
# server, so CI can run it on every push, on any runner.
#
# Usage:  EASYBLOBS_BASE=http://127.0.0.1:8080/artifacts bash e2e/hosted.sh generic
#
# Every assertion is "the bytes I published come back unchanged", which is the
# contract a cache/registry has to keep no matter which protocol fronts it.
set -euo pipefail

PROTO="${1:?usage: hosted.sh <protocol>}"
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
BASE="${EASYBLOBS_BASE%/}"

# A distinctive payload: if the server echoed something cached from elsewhere we
# would see it, and a byte-mangling bug cannot hide behind a valid status code.
PAYLOAD="easyblobs-hosted-$(date +%s)-$$"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

put() { # put <url> <file>
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X PUT --data-binary "@$2" "$1")
  case "$code" in
    200|201|204) ;;
    *) echo "FAIL: PUT $1 -> $code" >&2; return 1 ;;
  esac
}

get_expect() { # get_expect <url> <expected-file>
  code=$(curl -sS -o "$TMP/got" -w '%{http_code}' "$1")
  [ "$code" = "200" ] || { echo "FAIL: GET $1 -> $code" >&2; return 1; }
  if ! cmp -s "$TMP/got" "$2"; then
    echo "FAIL: GET $1 bytes differ from what was published" >&2
    echo "  want: $(head -c 80 "$2")" >&2
    echo "  got : $(head -c 80 "$TMP/got")" >&2
    return 1
  fi
}

printf '%s' "$PAYLOAD" > "$TMP/body"

case "$PROTO" in
  generic)
    # /artifacts/generic/{name}/{version}/{filename}
    U="$BASE/generic/e2e-pkg/1.0.0/e2e.bin"
    put "$U" "$TMP/body"
    get_expect "$U" "$TMP/body"
    ;;

  helm)
    # Helm is a hosted chart repo. The READ path is /charts/<file>; the WRITE
    # path is POST /api/charts with the packaged .tgz as the body (a REAL tgz:
    # the server parses Chart.yaml to derive name/version).
    CHART="$TMP/e2e-chart"
    mkdir -p "$CHART/templates"
    cat > "$CHART/Chart.yaml" <<'YAML'
apiVersion: v2
name: e2e-chart
version: 0.1.0
description: easyblobs hosted e2e
YAML
    cat > "$CHART/values.yaml" <<'YAML'
replicas: 1
YAML
    tar -C "$TMP" -czf "$TMP/e2e-chart-0.1.0.tgz" e2e-chart
    code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
      --data-binary "@$TMP/e2e-chart-0.1.0.tgz" "$BASE/helm/api/charts")
    case "$code" in
      200|201|204) ;;
      *) echo "FAIL: POST /api/charts -> $code" >&2; exit 1 ;;
    esac
    # The index must be regenerated and must name the chart we just uploaded.
    code=$(curl -sS -o "$TMP/index.yaml" -w '%{http_code}' "$BASE/helm/index.yaml")
    [ "$code" = "200" ] || { echo "FAIL: index.yaml -> $code" >&2; exit 1; }
    grep -q 'e2e-chart' "$TMP/index.yaml" || {
      echo "FAIL: uploaded chart missing from index.yaml" >&2
      cat "$TMP/index.yaml" >&2
      exit 1
    }
    # And the stored chart must download byte-identically.
    get_expect "$BASE/helm/charts/e2e-chart-0.1.0.tgz" "$TMP/e2e-chart-0.1.0.tgz"
    ;;

  ivy)
    # /artifacts/ivy/{host}/{path}
    U="$BASE/ivy/repo.e2e.invalid/org/example/e2e/1.0.0/e2e-1.0.0.jar"
    put "$U" "$TMP/body"
    get_expect "$U" "$TMP/body"
    ;;

  pypi)
    # PyPI needs a real wheel: the server parses it to derive name/version.
    # Build a minimal but VALID wheel (zip with a METADATA file).
    mkdir -p "$TMP/wheel/e2e_pkg-1.0.0.dist-info"
    cat > "$TMP/wheel/e2e_pkg-1.0.0.dist-info/METADATA" <<'META'
Metadata-Version: 2.1
Name: e2e-pkg
Version: 1.0.0
Summary: easyblobs hosted e2e
META
    printf 'value = 42\n' > "$TMP/wheel/e2e_pkg.py"
    # Build the wheel with Python's stdlib so the suite needs no `zip` binary.
    python3 - "$TMP/wheel" "$TMP/e2e_pkg-1.0.0-py3-none-any.whl" <<'PY'
import os, sys, zipfile
src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for root, _, files in os.walk(src):
        for f in files:
            p = os.path.join(root, f)
            z.write(p, os.path.relpath(p, src))
PY
    # PyPI's WRITE path is POST /upload with multipart fields (name, version,
    # file) - exactly what `twine upload` sends. The READ path is /simple/ plus
    # the package file URL the simple index advertises.
    code=$(curl -sS -o "$TMP/upload.json" -w '%{http_code}' -X POST \
      -F "name=e2e-pkg" -F "version=1.0.0" \
      -F "file=@$TMP/e2e_pkg-1.0.0-py3-none-any.whl;type=application/octet-stream" \
      "$BASE/pypi/upload")
    case "$code" in
      200|201|204) ;;
      *) echo "FAIL: POST /pypi/upload -> $code" >&2; cat "$TMP/upload.json" >&2; exit 1 ;;
    esac
    # /simple/ must now list the package.
    code=$(curl -sS -o "$TMP/simple.html" -w '%{http_code}' "$BASE/pypi/simple/e2e-pkg/")
    [ "$code" = "200" ] || { echo "FAIL: /simple/e2e-pkg/ -> $code" >&2; exit 1; }
    grep -q 'e2e_pkg-1.0.0-py3-none-any.whl' "$TMP/simple.html" || {
      echo "FAIL: uploaded wheel missing from the simple index" >&2
      cat "$TMP/simple.html" >&2
      exit 1
    }
    # Follow the URL the index ADVERTISES (relative to BASE), never a hand-built
    # one: pip uses exactly this href, so testing anything else would miss a
    # broken self_base.
    href=$(grep -o 'href="[^"]*"' "$TMP/simple.html" | head -1 | sed 's/href="//;s/"$//')
    [ -n "$href" ] || { echo "FAIL: no href in the simple index" >&2; exit 1; }
    # Strip the advertised origin (it may be a self_base or a localhost default)
    # and re-root at BASE so the check also works behind a port-forward.
    rel=${href#*"$BASE"}
    [ "$rel" != "$href" ] || rel="/pypi${href#*"/pypi"}"
    rel=${rel%%#*}   # drop #sha256=...
    get_expect "$BASE$rel" "$TMP/e2e_pkg-1.0.0-py3-none-any.whl"
    ;;

  *)
    echo "hosted.sh: unsupported protocol '$PROTO'" >&2
    exit 2
    ;;
esac

echo "hosted/$PROTO: OK"
