#!/bin/sh
# PECL (pecl.php.net): extension tarball download from the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/redis.tgz "$EASYBLOBS_BASE/get/redis"
tar -tzf /tmp/redis.tgz >/dev/null || { echo "pecl: bad tarball"; exit 1; }
echo "pecl: redis tarball ok"
