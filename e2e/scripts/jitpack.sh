#!/bin/sh
# JitPack mirrored at the named target (builds-on-demand Maven layout).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2j
rm -rf "$M2"; mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$EASYBLOBS_BASE" "$M2" > /tmp/settings-j.xml

mvn -B -q -s /tmp/settings-j.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -U -Dartifact=com.github.jitpack:maven-simple:1.4 -Dtransitive=false

jar=$(find "$M2" -name 'maven-simple-*.jar' | head -1)
[ -n "$jar" ] || { echo "jitpack: maven-simple jar not in the local repository"; exit 1; }
echo "jitpack: mvn resolved maven-simple 1.4 ($(wc -c <"$jar") bytes)"
