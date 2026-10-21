#!/bin/sh
# ci-server.sh — build and start easyblobs for a CI end-to-end run.
#
# Prints the two env vars the protocol scripts read, and blocks until /healthz
# answers so a client can never race the server.
#
#   eval "$(sh e2e/ci-server.sh)"   # sets EASYBLOBS_BASE / EASYBLOBS_URL
#
# EASYBLOBS_BASE  the /artifacts mount (every protocol except OCI)
# EASYBLOBS_URL   the origin (OCI is spec-fixed at /v2)
#
# --self-base is REQUIRED here: adapters embed it in the URLs they advertise
# (PyPI's /simple/ hrefs, npm tarball URLs, ...). Without it they emit a
# localhost:8080 default, and a real client would follow a dead link.
set -e

PORT="${PORT:-8080}"
DATA="${DATA:-/tmp/eb-data}"

rm -rf "$DATA"
mkdir -p "$DATA"

go build -o /tmp/easyblobs ./cmd

# A log file makes a hung/failed start diagnosable from the CI output.
/tmp/easyblobs --listen "127.0.0.1:$PORT" --data "$DATA" \
  --self-base "http://127.0.0.1:$PORT" >/tmp/easyblobs.log 2>&1 &
echo "$!" >/tmp/easyblobs.pid

i=0
until curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then
    echo "ci-server: easyblobs did not become healthy" >&2
    cat /tmp/easyblobs.log >&2
    exit 1
  fi
  sleep 0.5
done

echo "export EASYBLOBS_BASE=http://127.0.0.1:$PORT/artifacts"
echo "export EASYBLOBS_URL=http://127.0.0.1:$PORT"
echo "export EASYBLOBS_DATA=$DATA"
