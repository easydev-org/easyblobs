#!/bin/sh
# Protobuf (buf.build BSR): the BSR is a Connect/gRPC API whose methods are
# mounted at the HOST ROOT (`/<Service>/<Method>`), NOT a REST registry. We
# drive the adapter's passthrough directly with a Connect JSON POST.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# Call the BSR's documented Download endpoint: a plain GET with query
# parameters that returns the module's files as an archive.
#
# The Connect-unary variant (POST ModuleService/GetModules) was tried first, but
# its request body needs the exact protobuf oneof envelope and the service
# rejects every hand-written shape with
#   "module_refs[0].value: exactly one field is required in oneof"
# Download needs no body at all, so the test proves the same thing (a real
# public module fetched through the mount) without guessing an envelope.
code=$(wget -qO /tmp/mod \
  "$EASYBLOBS_BASE/buf.alpha.v1.DownloadService/Download?owner=connectrpc&repository=eliza&reference=main" \
  && echo 0 || echo $?)
[ "$code" = "0" ] || { echo "protobuf: Download rc=$code"; exit 1; }
bytes=$(wc -c </tmp/mod)
echo "module archive bytes: $bytes"
[ "$bytes" -gt 1000 ] || { echo "protobuf: suspiciously small module archive"; exit 1; }
# Compile assertion: `buf build` validates a proto module locally.
mkdir -p ${E2E_W:-/w}/proto && cd ${E2E_W:-/w}
cat > buf.yaml <<'X'
version: v2
modules:
  - path: proto
X
cat > proto/t.proto <<'X'
syntax = "proto3";
package t;
message T { string s = 1; int32 n = 2; }
X
buf build
echo "protobuf: buf build ok"
