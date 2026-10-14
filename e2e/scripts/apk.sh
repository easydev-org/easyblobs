#!/bin/sh
# Alpine apk: point /etc/apk/repositories at the artifact mount. The first path
# segment is the repo key (v3.24/main), matching the upstream layout.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
printf '%s/v3.24/main\n%s/v3.24/community\n' "$EASYBLOBS_BASE" "$EASYBLOBS_BASE" > /etc/apk/repositories
apk update
# --allow-untrusted: the artifact mirror serves the upstream index byte-for-byte
# but this test does not carry the Alpine signing keys.
apk add --no-cache --allow-untrusted jq
# Run assertion: execute the installed binary.
echo '{"ok":true}' | jq -e '.ok' >/dev/null
echo "apk installed: $(jq --version)"
