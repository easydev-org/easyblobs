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
# An aar IS a zip. `unzip` is not always installed in this image, so inspect the
# archive with the JVM that Maven itself runs on (java is guaranteed here): read
# the central directory through jshell-free java.util.zip.
if command -v unzip >/dev/null; then
  unzip -l "$aar" >/dev/null 2>&1 || { echo "google: the aar is not a readable archive"; exit 1; }
  unzip -l "$aar" 2>/dev/null | grep -q 'AndroidManifest.xml' || {
    echo "google: the aar has no AndroidManifest.xml"; unzip -l "$aar" | head -6; exit 1; }
  entries=$(unzip -l "$aar" | tail -1 | awk '{print $2}')
else
  cat > /tmp/AarCheck.java <<'JAVA'
import java.util.zip.*; import java.io.*;
public class AarCheck {
  public static void main(String[] a) throws Exception {
    boolean manifest = false; int n = 0;
    try (ZipFile z = new ZipFile(a[0])) {
      var e = z.entries();
      while (e.hasMoreElements()) {
        n++;
        if (e.nextElement().getName().equals("AndroidManifest.xml")) manifest = true;
      }
    }
    if (!manifest) { System.err.println("no AndroidManifest.xml"); System.exit(2); }
    System.out.println(n);
  }
}
JAVA
  javac -d /tmp /tmp/AarCheck.java 2>/dev/null
  entries=$(java -cp /tmp AarCheck "$aar") || {
    echo "google: the aar is not a readable archive (or lacks AndroidManifest.xml)"; exit 1; })
fi
echo "google: mvn resolved androidx.core:core:1.13.0 ($(wc -c <"$aar") bytes, $entries entries)"
