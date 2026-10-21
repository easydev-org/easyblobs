#!/bin/sh
# Spring milestone repository mirrored at the named target.
# Real Maven client (a milestone POM is exactly what trips naive parsers).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
M2=/tmp/m2s
rm -rf "$M2"
cat > /tmp/settings-s.xml <<XML
<settings>
  <localRepository>$M2</localRepository>
  <mirrors>
    <mirror><id>easyblobs</id><url>$EASYBLOBS_BASE</url><mirrorOf>*</mirrorOf></mirror>
  </mirrors>
</settings>
XML
mvn -B -q -s /tmp/settings-s.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=org.springframework:spring-core:6.2.0-M1
jar=$(find "$M2" -name 'spring-core-6.2.0-M1.jar' | head -1)
[ -n "$jar" ] || { echo "spring: spring-core jar not in the local repository"; exit 1; }
echo "spring: mvn resolved spring-core 6.2.0-M1 ($(wc -c <"$jar") bytes)"
