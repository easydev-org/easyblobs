#!/bin/sh
# JSR's npm-compatibility registry (npm.jsr.io), mirrored at the named target.
#
# REAL client: npm. `npm install @jsr/luca__flag` makes npm fetch the packument
# and then the tarball it advertises - so a success proves the packument's
# dist.tarball URL is usable, not merely present.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

command -v npm >/dev/null || { echo "jsrnpm: npm is missing"; exit 1; }

rm -rf /tmp/jsrnpm && mkdir -p /tmp/jsrnpm && cd /tmp/jsrnpm
echo '{"name":"e2e","version":"1.0.0","private":true}' > package.json

npm install --no-audit --no-fund --no-save \
  --registry="$EASYBLOBS_BASE/" \
  "@jsr/luca__flag@1.0.1" 2>&1 | tail -5

test -d node_modules/@jsr/luca__flag || {
  echo "jsrnpm: npm did not install @jsr/luca__flag"; exit 1; }
echo "jsrnpm: npm installed @jsr/luca__flag@1.0.1 from the mirror"
