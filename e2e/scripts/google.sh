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
# The aar must be an archive with a classes.jar inside - that is what the
# Android build consumes. `unzip -t` reports "extra bytes" for some aar
# variants, so test the CONTENT instead of the CRC table.
unzip -l "$aar" 2>/dev/null | grep -q 'classes.jar' || {
  echo "google: the aar does not contain classes.jar"; unzip -l "$aar" | head -5; exit 1; }
echo "google: mvn resolved androidx.core:core:1.13.0 ($(wc -c <"$aar") bytes)"
