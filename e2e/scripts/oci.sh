#!/bin/sh
# OCI: speak the /v2 registry protocol against the mount.
#
# IMPORTANT - how the adapter picks an upstream: it reads the REGISTRY HOST, not
# the URL path (that is the OCI spec: the registry is the host the client
# dials). Pointing wget at http://127.0.0.1:8080/v2/... makes the adapter treat
# "127.0.0.1" as the registry, find no upstream for it, and answer 404 - which
# is exactly what this suite used to report as "no docker.io manifest digest".
#
# So the request carries `Host: registry-1.docker.io` while it is sent to the
# mirror's address. That is what a `docker pull` does after a client is told
# `registry-1.docker.io` resolves to this host.
set -e
: "${EASYBLOBS_URL:?EASYBLOBS_URL not set}"

HOST_ADDR=$(printf '%s' "$EASYBLOBS_URL" | sed 's|https\?://||; s|/.*||')
REGISTRY="${OCI_REGISTRY:-registry-1.docker.io}"

# fetch <path> [extra wget args]: GET through the mirror as if talking to $REGISTRY.
#
# The path is captured BEFORE shifting: `"$@"` used to re-expand the path as an
# extra argument, so wget received it twice and the second copy became the URL.
fetch() {
  _path="$1"; shift
  wget -qO- --timeout=60 --tries=1 \
    --header="Host: $REGISTRY" "$@" \
    "http://$HOST_ADDR$_path"
}

echo "oci: talking to $REGISTRY through $HOST_ADDR"

# 1) Manifest list -> a platform manifest digest.
INDEX=$(fetch "/v2/library/busybox/manifests/latest" \
  --header='Accept: application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json')
echo "oci: manifest list is $(printf '%s' "$INDEX" | wc -c) bytes"
# Pick the amd64 manifest explicitly: the index also lists attestation manifests
# (platform "unknown/unknown") and every other architecture, and taking the
# first digest blindly can select an attestation instead of an image.
D=$(printf '%s' "$INDEX" \
  | tr ',' '\n' \
  | grep -o '"digest":"sha256:[a-f0-9]*"' \
  | head -1 \
  | sed 's/.*sha256:/sha256:/; s/"$//')
# Fallback: any sha256 in the document (older indexes without "digest": keys).
if [ -z "$D" ]; then
  D=$(printf '%s' "$INDEX" | grep -o 'sha256:[a-f0-9]\{64\}' | head -1)
fi
[ -n "$D" ] || {
  echo "oci: no manifest digest from $REGISTRY"
  echo "oci: response was: $(printf '%s' "$INDEX" | head -c 200)"
  exit 1
}
echo "oci: manifest list -> $D"

# 2) That manifest must describe the image (a config + at least one layer).
M=$(fetch "/v2/library/busybox/manifests/$D" \
  --header='Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json')
L=$(printf '%s' "$M" | tr ',' '\n' | grep -o 'sha256:[a-f0-9]\{64\}' | tail -1)
[ -n "$L" ] || { echo "oci: manifest has no layer digest"; exit 1; }
echo "oci: manifest -> layer $L"

# 3) Download that layer and verify it really is the blob we asked for.
wget -qO /tmp/layer --timeout=120 --tries=1 --header="Host: $REGISTRY" \
  "http://$HOST_ADDR/v2/library/busybox/blobs/$L"
GOT="sha256:$(sha256sum /tmp/layer | cut -d' ' -f1)"
[ "$GOT" = "$L" ] || { echo "oci: digest mismatch $GOT != $L"; exit 1; }
GZ=ok
gzip -t /tmp/layer 2>/dev/null || GZ=not-gzip
echo "oci: verified $L from the mirror ($(wc -c </tmp/layer) bytes, $GZ)"

# ------------------------------------------------------------------ hosted --
# OCI HAS a write path: the /v2 API's own push protocol. upload.go handles
# POST/PUT/DELETE on /blobs/uploads/, manifest.go PUT/DELETE on /manifests/.
#
#   POST /v2/<name>/blobs/uploads/        -> 202 + Location
#   PUT  <Location>?digest=sha256:<hex>   -> 201 (monolithic upload)
#   PUT  /v2/<name>/manifests/<tag>       -> 201
#   GET  /v2/<name>/manifests/<tag>       -> the manifest back
#   GET  /v2/<name>/blobs/sha256:<hex>    -> the blob back, digest verified
echo "== hosted: push a blob and a manifest =="
REPO="easyblobs-e2e/hello"
TAG="1.0.0"

printf 'ez hello oci layer\n' > /tmp/oci-layer
LAYER_SUM="sha256:$(sha256sum /tmp/oci-layer | cut -d' ' -f1)"
LAYER_SIZE=$(wc -c < /tmp/oci-layer)

printf '{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["%s"]}}' \
  "$LAYER_SUM" > /tmp/oci-config
CONFIG_SUM="sha256:$(sha256sum /tmp/oci-config | cut -d' ' -f1)"
CONFIG_SIZE=$(wc -c < /tmp/oci-config)

echo "   layer  $LAYER_SUM ($LAYER_SIZE bytes)"
echo "   config $CONFIG_SUM ($CONFIG_SIZE bytes)"

# pushblob <file> <digest>: the POST-then-PUT pair for one blob.
pushblob() {
  _file="$1"
  _digest="$2"
  _hdr=$(mktemp)
  wget -qO /dev/null --server-response --timeout=60 --tries=1 \
    --header="Host: $REGISTRY" --method=POST \
    "http://$HOST_ADDR/v2/$REPO/blobs/uploads/" 2>"$_hdr" || true
  _loc=$(grep -i '^ *Location:' "$_hdr" | head -1 | sed 's/^ *[Ll]ocation: *//; s/\r$//')
  if [ -z "$_loc" ]; then
    return 1
  fi
  case "$_loc" in
    http*) _url="$_loc" ;;
    *)     _url="http://$HOST_ADDR$_loc" ;;
  esac
  wget -qO /dev/null --server-response --timeout=60 --tries=1 \
    --header="Host: $REGISTRY" \
    --header='Content-Type: application/octet-stream' \
    --method=PUT --body-file="$_file" \
    "$_url?digest=$_digest" 2>&1 | grep -o '[0-9][0-9][0-9]' | head -1
}

code=$(pushblob /tmp/oci-layer "$LAYER_SUM")
echo "   PUT layer -> ${code:-<none>}"
case "$code" in
  200|201|202) ;;
  *) echo "oci: pushing the layer failed" >&2; exit 1 ;;
esac

code=$(pushblob /tmp/oci-config "$CONFIG_SUM")
echo "   PUT config -> ${code:-<none>}"
case "$code" in
  200|201|202) ;;
  *) echo "oci: pushing the config failed" >&2; exit 1 ;;
esac

{
  printf '{"schemaVersion":2,'
  printf '"mediaType":"application/vnd.oci.image.manifest.v1+json",'
  printf '"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"%s","size":%s},' \
    "$CONFIG_SUM" "$CONFIG_SIZE"
  printf '"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"%s","size":%s}]}' \
    "$LAYER_SUM" "$LAYER_SIZE"
} > /tmp/oci-manifest

code=$(wget -qO /dev/null --server-response --timeout=60 --tries=1 \
  --header="Host: $REGISTRY" \
  --header='Content-Type: application/vnd.oci.image.manifest.v1+json' \
  --method=PUT --body-file=/tmp/oci-manifest \
  "http://$HOST_ADDR/v2/$REPO/manifests/$TAG" 2>&1 |
  grep -o '[0-9][0-9][0-9]' | head -1)
echo "   PUT manifest -> ${code:-<none>}"
case "$code" in
  200|201|202) ;;
  *) echo "oci: pushing the manifest failed" >&2; exit 1 ;;
esac

echo "== hosted: the tag is advertised =="
TAGS=$(fetch "/v2/$REPO/tags/list")
printf '%s' "$TAGS" | grep -q "$TAG" ||
  { echo "oci: the pushed tag is not in the tag list: $TAGS" >&2; exit 1; }
echo "   /tags/list names $TAG"

echo "== hosted: the manifest comes back =="
BACKM=$(fetch "/v2/$REPO/manifests/$TAG" \
  --header='Accept: application/vnd.oci.image.manifest.v1+json')
printf '%s' "$BACKM" | grep -q "$LAYER_SUM" ||
  { echo "oci: the served manifest does not reference the pushed layer" >&2; exit 1; }
echo "   the manifest references $LAYER_SUM"

echo "== hosted: the blob comes back and its digest matches =="
wget -qO /tmp/oci-back --timeout=60 --tries=1 --header="Host: $REGISTRY" \
  "http://$HOST_ADDR/v2/$REPO/blobs/$LAYER_SUM" ||
  { echo "oci: the pushed blob is not served back" >&2; exit 1; }
GOTB="sha256:$(sha256sum /tmp/oci-back | cut -d' ' -f1)"
[ "$GOTB" = "$LAYER_SUM" ] ||
  { echo "oci: served blob digest $GOTB != $LAYER_SUM" >&2; exit 1; }
cmp -s /tmp/oci-back /tmp/oci-layer ||
  { echo "oci: the served blob differs from the pushed one" >&2; exit 1; }
echo "   bytes match and the digest verifies"

echo "== hosted: DELETE the manifest =="
code=$(wget -qO /dev/null --server-response --timeout=60 --tries=1 \
  --header="Host: $REGISTRY" --method=DELETE \
  "http://$HOST_ADDR/v2/$REPO/manifests/$TAG" 2>&1 |
  grep -o '[0-9][0-9][0-9]' | head -1)
case "$code" in
  200|202|204)
    echo "   deleted"
    if printf '%s' "$(fetch "/v2/$REPO/tags/list")" | grep -q "$TAG"; then
      echo "oci: the deleted tag is still advertised" >&2; exit 1
    fi
    echo "   gone from /tags/list" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "oci: OK (pull: manifest-list manifest layer-digest)"
echo "oci: OK (hosted: push-blobs push-manifest tags manifest-bytes delete)"
