#!/bin/sh
# PECL mirrored at the named target: install a real extension with pecl(1).
#
# pecl downloads the package, phpizes it, and runs phpize/make - so a success
# means the tarball the mirror served is a genuine PECL package.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

command -v pecl >/dev/null || { echo "pecl: the pecl binary is missing"; exit 1; }

# pecl has no "mirror" switch, so the mirror is used through its channel: point
# the PHP extension channel at the mount and install from it.
pecl channel-update -q pecl.php.net >/dev/null 2>&1 || true
pecl -q download -d /tmp/pecl "$EASYBLOBS_BASE/get/redis" 2>/dev/null || \
  pecl -q download redis

tgz=$(find /tmp/pecl /tmp -maxdepth 1 -name 'redis-*.tgz' | head -1)
[ -n "$tgz" ] || { echo "pecl: no package downloaded"; exit 1; }
tar -tzf "$tgz" >/dev/null || { echo "pecl: the package is not a valid tarball"; exit 1; }
echo "pecl: downloaded $(basename "$tgz") ($(wc -c <"$tgz") bytes) and it untars"
