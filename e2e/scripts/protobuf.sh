#!/bin/sh
# Protobuf (buf.build BSR): the BSR is a Connect/gRPC API whose methods are
# mounted at the HOST ROOT (`/<Service>/<Method>`), NOT a REST registry. We
# drive the adapter's passthrough directly with a Connect JSON POST.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# Call a real BSR method through artifact (public module read, no auth needed).
code=$(wget -qO /tmp/mod --header='Content-Type: application/json' \
  --post-data='{"moduleRefs":[{"moduleRef":{"module":"buf.build/connectrpc/eliza"}}]}' \
  "$EASYBLOBS_BASE/buf.registry.module.v1.ModuleService/GetModules" && echo 0 || echo $?)
[ "$code" = "0" ] || { echo "protobuf: GetModules rc=$code"; exit 1; }
echo "module response bytes: $(wc -c </tmp/mod)"
test -s /tmp/mod
# Compile assertion: `buf build` validates a proto module locally.
mkdir -p /w/proto && cd /w
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
