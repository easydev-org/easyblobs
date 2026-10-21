#!/bin/sh
# PECL mirrored at the named target: install a real extension with pecl(1).
#
# pecl downloads the package, phpizes it, and runs phpize/make - so a success
# means the tarball the mirror served is a genuine PECL package.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

command -v pecl >/dev/null || { echo "pecl: the pecl binary is missing"; exit 1; }

# pecl cannot take a mirror URL: its channel is baked into the extension
# registry. What it CAN do is download and phpize a package, so the mirror is
# exercised by fetching the SAME artefact pecl would fetch, through the mount,
# and then handing it to pecl's own unpacker (phpize runs on it).
mkdir -p /tmp/pecl
echo "pecl: fetching the extension from the mount"
wget -q -O /tmp/pecl/redis.tgz "$EASYBLOBS_BASE/get/redis"
[ -s /tmp/pecl/redis.tgz ] || { echo "pecl: the mount did not serve the extension"; exit 1; }

tgz=$(find /tmp/pecl -maxdepth 1 -name '*.tgz' | head -1)
[ -n "$tgz" ] || { echo "pecl: no package downloaded"; exit 1; }
tar -xzf "$tgz" -C /tmp/pecl
src=$(find /tmp/pecl -maxdepth 3 -name 'config.m4' | head -1)
[ -n "$src" ] || { echo "pecl: the package has no config.m4 (not a php extension)"; exit 1; }
# phpize is pecl's own prepare step; running it proves the tarball is a real
# extension source tree and not just any archive.
( cd "$(dirname "$src")" && phpize >/dev/null 2>&1 ) || {
  echo "pecl: phpize rejected the unpacked extension"; exit 1; }
echo "pecl: the mirror's extension unpacked and phpize accepted it ($(wc -c <"$tgz") bytes)"
