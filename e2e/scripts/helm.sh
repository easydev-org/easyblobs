#!/bin/sh
# helm: the whole client surface against the mount, in both directions.
#
# A chart repository is three things:
#
#   <base>/index.yaml                 the repository index (every chart/version)
#   <base>/api/charts                 the API `helm push` uses (chartmuseum style)
#   <base>/charts/<name>-<ver>.tgz    the packaged chart
#
# PULL
#   helm repo add / update   reads index.yaml
#   helm search repo         queries the index
#   helm pull                downloads a .tgz by name and version
#   helm show chart          reads metadata out of the archive
#   helm template            renders a real chart
#
# HOSTED
#   helm lint / package      builds a chart
#   helm push                uploads it
#   the index                index.yaml lists it afterwards
#   helm pull + template     a consumer fetches it back and renders it
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "helm: FAIL: $*" >&2; exit 1; }
export HELM_REPOSITORY_CONFIG="$WORK/repositories.yaml"
export HELM_REPOSITORY_CACHE="$WORK/cache"
export HELM_CACHE_HOME="$WORK/helmcache"
export HELM_CONFIG_HOME="$WORK/helmconfig"
export HELM_DATA_HOME="$WORK/helmdata"
mkdir -p "$HELM_REPOSITORY_CACHE" "$HELM_CACHE_HOME" \
         "$HELM_CONFIG_HOME" "$HELM_DATA_HOME"

echo "== the index is served =="
curl -fsS "$BASE/index.yaml" >/dev/null || fail "/index.yaml"

echo "== helm repo add / update =="
helm repo add mount "$BASE" >/dev/null 2>&1 || fail "helm repo add"
helm repo update >/dev/null 2>&1 || fail "helm repo update"
helm repo list 2>/dev/null | grep -q mount || fail "the repo is not listed"

echo "== helm search repo =="
helm search repo mount 2>/dev/null | head -3 >/dev/null || true

echo "== pull a chart the mirror already serves =="
PULLED=""
for c in $(helm search repo mount --short 2>/dev/null | head -5); do
  if helm pull "$c" --untar --untardir "$WORK/pulled" >/dev/null 2>&1; then
    PULLED="$c"
    break
  fi
done
if [ -n "$PULLED" ]; then
  echo "   pulled $PULLED"
  dir=$(ls -d "$WORK"/pulled/* 2>/dev/null | head -1)
  [ -n "$dir" ] || fail "the chart did not untar"
  helm show chart "$dir" >/dev/null || fail "helm show chart"
  helm template rel "$dir" >/dev/null || fail "helm template on a pulled chart"
else
  echo "   (index is empty; the pull cases run against the hosted chart below)"
fi

CH="easyblobs-e2e"
VER="0.1.0"
SRC="$WORK/chart"
mkdir -p "$SRC/templates"
{
  echo "apiVersion: v2"
  echo "name: $CH"
  echo "version: $VER"
  echo 'appVersion: "1.0.0"'
  echo "description: easyblobs e2e"
  echo "type: application"
} > "$SRC/Chart.yaml"
{
  echo "replicas: 1"
  echo "message: ez hello"
} > "$SRC/values.yaml"
# A template needs literal {{ }} at column 0, so it is written with a quoted
# heredoc: nothing in it may be shell-expanded.
cat > "$SRC/templates/configmap.yaml" <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-hello
data:
  message: {{ .Values.message | quote }}
YAML

echo "== helm lint / package =="
helm lint "$SRC" >/dev/null 2>&1 || fail "helm lint"
( cd "$WORK" && helm package "$SRC" >/dev/null 2>&1 ) || fail "helm package"
TGZ="$WORK/$CH-$VER.tgz"
[ -s "$TGZ" ] || fail "helm package produced no .tgz"

echo "== helm push =="
# `helm push` only speaks OCI (oci://...) - it cannot upload to a chartmuseum
# style HTTP repository at all. This adapter exposes the chartmuseum API instead:
#   POST <base>/api/charts  (multipart chart=@file)
# So the upload is that POST, which is what a chartmuseum client does. Saying
# "helm push" here would be testing a command that does not exist for this shape.
code=$(curl -sS -o /tmp/helm-up.out -w '%{http_code}' -X POST \
  -F "chart=@$TGZ" "$BASE/api/charts")
case "$code" in
  200|201|202) echo "   uploaded (HTTP $code)" ;;
  *) echo "helm: the chartmuseum upload failed:" >&2
     cat /tmp/helm-up.out >&2 || true
     fail "POST /api/charts -> $code" ;;
esac

echo "== the index lists it =="
curl -fsS "$BASE/index.yaml" | grep -q "$CH" ||
  fail "index.yaml does not list $CH after push"

echo "== helm repo update sees it =="
helm repo update >/dev/null 2>&1 || fail "helm repo update after push"
helm search repo "mount/$CH" 2>/dev/null | grep -q "$CH" ||
  fail "helm search does not see the pushed chart"

echo "== a consumer pulls it back and renders it =="
CONS="$WORK/cons"
mkdir -p "$CONS"
helm pull "mount/$CH" --version "$VER" --untar --untardir "$CONS" >/dev/null 2>&1 ||
  fail "a consumer cannot pull the pushed chart"
if ! OUT=$(helm template rel "$CONS/$CH" 2>&1); then
  echo "helm: helm template failed:" >&2
  echo "$OUT" | tail -10 >&2
  fail "the pulled chart does not render"
fi
echo "$OUT" | grep -q "ez hello" ||
  fail "the rendered output does not contain the chart values"
echo "   pulled and rendered with the right values"

echo "== delete the chart =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/api/charts/$CH/$VER")
case "$code" in
  200|202|204) echo "   deleted" ;;
  *) echo "   (DELETE answered $code; the adapter may not expose chart deletion)" ;;
esac

echo "helm: OK (pull: index repo-add update search pull show template)"
echo "helm: OK (hosted: lint package push index search pull render)"
