#!/bin/sh
# Spring milestone repository mirrored at the named target.
# A milestone POM is exactly what trips naive parsers, so Maven must read it.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2s
rm -rf "$M2"; mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$EASYBLOBS_BASE" "$M2" > /tmp/settings-s.xml

mvn -B -q -s /tmp/settings-s.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -U -Dartifact=org.springframework:spring-core:6.2.0-M1 -Dtransitive=false

jar=$(find "$M2" -name 'spring-core-6.2.0-M1.jar' | head -1)
[ -n "$jar" ] || { echo "spring: spring-core jar not in the local repository"; exit 1; }
echo "spring: mvn resolved spring-core 6.2.0-M1 ($(wc -c <"$jar") bytes)"
