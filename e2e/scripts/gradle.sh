#!/bin/sh
# Gradle Plugin Portal: a Maven-layout mirror at the named target mount.
#
# Uses the REAL Maven client, not wget: `mvn dependency:get` resolves the
# artifact through the mirror and leaves it in the local repository, so a
# success means Maven itself accepted what the mirror served (valid POM, valid
# jar, matching checksums when present).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2
rm -rf "$M2"
cat > /tmp/settings.xml <<XML
<settings>
  <localRepository>$M2</localRepository>
  <mirrors>
    <mirror>
      <id>easyblobs</id>
      <name>easyblobs mirror</name>
      <url>$EASYBLOBS_BASE</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
XML

mvn -B -q -s /tmp/settings.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=org.jetbrains.kotlin:kotlin-stdlib:2.0.0

# Maven only reports success when it actually stored the artifact.
jar=$(find "$M2" -name 'kotlin-stdlib-2.0.0.jar' | head -1)
[ -n "$jar" ] || { echo "gradle: kotlin-stdlib jar not in the local repository"; exit 1; }
echo "gradle: mvn resolved kotlin-stdlib 2.0.0 ($(wc -c <"$jar") bytes)"
