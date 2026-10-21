#!/bin/sh
# Google Maven (dl.google.com): AGP + androidx, mirrored at the named target.
#
# Real Maven client: it has to parse the POM and fetch the AAR through the
# mirror, which a wget probe never proves.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2g
rm -rf "$M2"
cat > /tmp/settings-g.xml <<XML
<settings>
  <localRepository>$M2</localRepository>
  <mirrors>
    <mirror>
      <id>easyblobs</id>
      <url>$EASYBLOBS_BASE</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
XML

mvn -B -q -s /tmp/settings-g.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -Dartifact=androidx.core:core:1.13.0:aar -Dtransitive=false

aar=$(find "$M2" -name 'core-1.13.0.aar' | head -1)
[ -n "$aar" ] || { echo "google: androidx core aar not in the local repository"; exit 1; }
unzip -t "$aar" >/dev/null || { echo "google: the aar is not a valid zip"; exit 1; }
echo "google: mvn resolved androidx.core:core:1.13.0 ($(wc -c <"$aar") bytes)"
