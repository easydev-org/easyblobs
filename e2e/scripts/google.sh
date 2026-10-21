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
# An aar IS a zip, but newer ones may ship classes.jar under a different name
# (or as a jar/ dir entry), so assert it is a readable archive with entries
# rather than a specific member name.
entries=$(unzip -l "$aar" 2>/dev/null | tail -1 | awk '{print $2}')
[ -n "$entries" ] && [ "$entries" -gt 0 ] || {
  echo "google: the aar is not a readable archive"; unzip -l "$aar" | head -5; exit 1; }
# AndroidManifest.xml is present in every aar and is what AGP threads through.
unzip -l "$aar" 2>/dev/null | grep -q 'AndroidManifest.xml' || {
  echo "google: the aar has no AndroidManifest.xml"; unzip -l "$aar" | head -6; exit 1; }
echo "google: mvn resolved androidx.core:core:1.13.0 ($(wc -c <"$aar") bytes, $entries entries)"
