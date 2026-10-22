#!/bin/sh
# Clojars (a Maven-layout repository) mirrored at the named target.
# Real Maven client: Maven must parse the POM and store the jar.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2c
rm -rf "$M2"; mkdir -p "$M2"
# The mount is a REPOSITORY, not a `mirrorOf: *` - otherwise Maven's own
# dependency plugin would also be redirected to a repo that does not carry it.
sh "$(dirname "$0")/../maven-settings.sh" "$EASYBLOBS_BASE" "$M2" > /tmp/settings-c.xml

mvn -B -q -s /tmp/settings-c.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -U -Dartifact=ring:ring-core:1.11.0 -Dtransitive=false

jar=$(find "$M2" -name 'ring-core-1.11.0.jar' | head -1)
[ -n "$jar" ] || { echo "clojars: ring-core jar not in the local repository"; exit 1; }
echo "clojars: mvn resolved ring:ring-core:1.11.0 ($(wc -c <"$jar") bytes)"
