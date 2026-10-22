#!/bin/sh
# Google Maven (dl.google.com): AGP + androidx, mirrored at the named target.
#
# Real Maven client: it has to parse the POM and fetch the AAR through the
# mount, which a wget probe never proves.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

M2=/tmp/m2g
rm -rf "$M2"; mkdir -p "$M2"
# The mount is a REPOSITORY, not a `mirrorOf: *`: a mirror of only Google
# artifacts cannot serve Maven's own dependency plugin, so `mirrorOf: *` fails
# before the artifact under test is even requested.
sh "$(dirname "$0")/../maven-settings.sh" "$EASYBLOBS_BASE" "$M2" > /tmp/settings-g.xml

mvn -B -q -s /tmp/settings-g.xml \
  org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
  -U -Dartifact=androidx.core:core:1.13.0:aar -Dtransitive=false

aar=$(find "$M2" -name 'core-1.13.0.aar' | head -1)
[ -n "$aar" ] || { echo "google: androidx core aar not in the local repository"; exit 1; }

# An aar IS a zip. `unzip` may not exist, so fall back to java.util.zip, which
# is guaranteed because Maven itself runs on the JVM.
if command -v unzip >/dev/null; then
  unzip -l "$aar" 2>/dev/null | grep -q 'AndroidManifest.xml' || {
    echo "google: the aar has no AndroidManifest.xml"; unzip -l "$aar" | head -6; exit 1; }
  entries=$(unzip -l "$aar" | tail -1 | awk '{print $2}')
else
  cat > /tmp/AarCheck.java <<'JAVA'
import java.util.zip.*;
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
    echo "google: the aar is not a readable archive (or lacks AndroidManifest.xml)"; exit 1; }
fi
echo "google: mvn resolved androidx.core:core:1.13.0 ($(wc -c <"$aar") bytes, $entries entries)"
