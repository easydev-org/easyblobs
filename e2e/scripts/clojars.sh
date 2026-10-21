#!/bin/sh
# Clojars (a Maven-layout repository) mirrored at the named target.
# Real Maven client: POM parsing + jar storage must both succeed.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
M2=/tmp/m2c
rm -rf "$M2"
cat > /tmp/settings-c.xml <<XML
<settings>
  <localRepository>$M2</localRepository>
  <mirrors>
    <mirror><id>easyblobs</id><url>$EASYBLOBS_BASE</url><mirrorOf>*</mirrorOf></mirror>
  </mirrors>
</settings>
XML
mvn -B -q -s /tmp/settings-c.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=ring:ring-core:1.11.0 -Dtransitive=false
jar=$(find "$M2" -name 'ring-core-1.11.0.jar' | head -1)
[ -n "$jar" ] || { echo "clojars: ring-core jar not in the local repository"; exit 1; }
echo "clojars: mvn resolved ring:ring-core:1.11.0 ($(wc -c <"$jar") bytes)"
