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
fetch() {
  wget -qO- --timeout=60 --tries=1 \
    --header="Host: $REGISTRY" "$@" \
    "http://$HOST_ADDR$1"
}

echo "oci: talking to $REGISTRY through $HOST_ADDR"

# 1) Manifest list -> a platform manifest digest.
INDEX=$(fetch "/v2/library/busybox/manifests/latest" \
  --header='Accept: application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json')
D=$(printf '%s' "$INDEX" | tr '}' '\n' | grep -o 'sha256:[a-f0-9]\{64\}' | head -1)
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
