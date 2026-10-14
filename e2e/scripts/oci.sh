#!/bin/sh
# OCI: point the client at the artifact registry endpoint. A docker/podman
# client carries the target registry in the Host header (never the path), so
# the artifact Service host IS the registry for this run; the adapter maps it
# to the default upstream (Docker Hub). We speak the /v2 protocol directly.
set -e
: "${EASYBLOBS_URL:?EASYBLOBS_URL not set}"
REG="$EASYBLOBS_URL"

# fetch_manifest <repo> <tag>: pull the manifest list and print the first
# platform digest, proving the registry resolved through the mirror.
fetch_manifest() {
  wget -qO- --header='Accept: application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json' \
    "$REG/v2/$1/manifests/$2" | tr '}' '\n' | grep -o 'sha256:[a-f0-9]\{64\}' | head -1
}

# Docker Hub: full path (manifest list -> amd64 manifest -> layer blob).
D=$(fetch_manifest library/busybox latest)
[ -n "$D" ] || { echo "oci: no docker.io manifest digest"; exit 1; }
M=$(wget -qO- --header='Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
  "$REG/v2/library/busybox/manifests/$D")
L=$(printf '%s' "$M" | tr ',' '\n' | grep -o 'sha256:[a-f0-9]\{64\}' | tail -1)
wget -qO /tmp/layer "$REG/v2/library/busybox/blobs/$L"
GOT="sha256:$(sha256sum /tmp/layer | cut -d' ' -f1)"
[ "$GOT" = "$L" ] || { echo "oci: digest mismatch $GOT != $L"; exit 1; }
gzip -t /tmp/layer
echo "oci: verified $L from the mirror"
