#!/bin/sh
# Debian/apt: point the source at the artifact mount. The mount's first path
# segment is the archive key (e.g. "debian"), matching the upstream layout.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
mkdir -p /etc/apt/sources.list.d
rm -f /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list
printf 'deb [trusted=yes] %s/debian bookworm main\n' "$EASYBLOBS_BASE" > /etc/apt/sources.list.d/artifact.list
apt-get update -o Acquire::Retries=0
apt-get install -y --no-install-recommends jq
# Run assertion: execute the installed binary and use its JSON parser.
echo '{"ok":true}' | jq -e '.ok' >/dev/null
echo "apt install ran: $(jq --version)"
