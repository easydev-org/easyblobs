#!/bin/sh
# Helm: add the artifact mount as a chart repository (index.yaml + charts/).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
helm repo add easy-platform "$EASYBLOBS_BASE" >/dev/null
helm repo update >/dev/null 2>&1
helm pull easy-platform/redis -d /tmp
# Parse assertion: render the pulled chart's templates.
cd /tmp
helm template easy redis-*.tgz >/dev/null
echo "helm: rendered $(ls redis-*.tgz)"
