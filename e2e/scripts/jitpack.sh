#!/bin/sh
# JitPack mirrored at the named target (builds-on-demand Maven layout).
# Real Maven client against a SNAPSHOT coordinate, which needs snapshot
# metadata handling that a wget probe does not exercise.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
M2=/tmp/m2j
rm -rf "$M2"
cat > /tmp/settings-j.xml <<XML
<settings>
  <localRepository>$M2</localRepository>
  <mirrors>
    <mirror><id>easyblobs</id><url>$EASYBLOBS_BASE</url><mirrorOf>*</mirrorOf></mirror>
  </mirrors>
</settings>
XML
# A SNAPSHOT coordinate needs the timestamped version, which Maven resolves from
# maven-metadata.xml. Ask for the POM first (that is what a build does), then
# the jar for the resolved snapshot.
mvn -B -q -s /tmp/settings-j.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=com.github.jitpack:maven-simple:1.4:pom -Dtransitive=false \
  || true
mvn -B -q -s /tmp/settings-j.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=com.github.jitpack:maven-simple:1.4 -Dtransitive=false
jar=$(find "$M2" -name 'maven-simple-*.jar' | head -1)
[ -n "$jar" ] || { echo "jitpack: maven-simple jar not in the local repository"; exit 1; }
echo "jitpack: mvn resolved maven-simple 1.4 ($(wc -c <"$jar") bytes)"
