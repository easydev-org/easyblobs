#!/bin/sh
# Gradle Plugin Portal: a Maven-layout mirror at the named target mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2
rm -rf "$M2"; mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$EASYBLOBS_BASE" "$M2" > /tmp/settings.xml

mvn -B -q -s /tmp/settings.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -U -Dartifact=org.jetbrains.kotlin:kotlin-stdlib:2.0.0 -Dtransitive=false

jar=$(find "$M2" -name 'kotlin-stdlib-2.0.0.jar' | head -1)
[ -n "$jar" ] || { echo "gradle: kotlin-stdlib jar not in the local repository"; exit 1; }
echo "gradle: mvn resolved kotlin-stdlib 2.0.0 ($(wc -c <"$jar") bytes)"
