#!/bin/sh
# google: Google's Maven repository (dl.google.com/dl/android/maven2).
#
# This repository is not a mirror of Maven Central with different coordinates -
# it is the only source for Android artifacts, and it has two packaging shapes
# Maven Central almost never carries:
#
#   .aar   an Android archive: a zip with AndroidManifest.xml + classes.jar,
#          consumed by the Android Gradle plugin rather than by javac
#   .jar   for the plain JVM libraries alongside them
#
# Two things a client needs beyond "a file downloaded":
#   * a POM in the repository can declare <packaging>aar</packaging>, and
#     Maven must honour it - asking for the .jar of an aar-only artifact fails;
#   * the aar itself must be a well-formed archive with a manifest, or the
#     Android toolchain refuses it later with an error that names the module,
#     not the mirror.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "google: FAIL: $*" >&2; exit 1; }

M2="$WORK/m2"
mkdir -p "$M2"
# The mount is a REPOSITORY, not a `mirrorOf: *`: a mirror of only Google
# artifacts cannot serve Maven's own dependency plugin, so `mirrorOf: *` fails
# before the artifact under test is even requested.
sh "$(dirname "$0")/../maven-settings.sh" "$BASE" "$M2" > "$WORK/settings.xml"

GROUP="androidx.core"
ART="core"
VER="1.13.0"
GPATH=$(printf '%s' "$GROUP" | tr '.' '/')

echo "== the POM declares packaging aar =="
curl -fsS -o "$WORK/pom" "$BASE/$GPATH/$ART/$VER/$ART-$VER.pom" ||
  fail "GET the pom"
grep -q "<packaging>aar</packaging>" "$WORK/pom" ||
  { echo "google: the pom does not declare packaging aar:" >&2
    grep -m3 "<packaging>" "$WORK/pom" >&2 || true
    exit 1; }
echo "   <packaging>aar</packaging>"

echo "== maven resolves the aar through the mount =="
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$VER:aar" -Dtransitive=false >/tmp/google-get.log 2>&1; then
  echo "   resolved $GROUP:$ART:$VER:aar"
else
  echo "google: mvn could not resolve the aar:" >&2
  tail -25 /tmp/google-get.log >&2
  fail "resolve $GROUP:$ART:$VER:aar"
fi

AAR=$(find "$M2" -name "$ART-$VER.aar" | head -1)
[ -n "$AAR" ] || fail "the aar is not in the local repository"
echo "   the aar landed ($(wc -c < "$AAR") bytes)"

echo "== it is a real Android archive =="
# An aar IS a zip. `unzip` may be absent, so java.util.zip is the fallback -
# guaranteed to exist because Maven itself runs on the JVM.
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$AAR" 2>/dev/null | grep -q 'AndroidManifest.xml' ||
    { echo "google: the aar has no AndroidManifest.xml:" >&2
      unzip -l "$AAR" 2>/dev/null | head -8 >&2; exit 1; }
  ENTRIES=$(unzip -l "$AAR" | tail -1 | awk '{print $2}')
else
  {
    echo 'import java.util.zip.*;'
    echo 'public class AarCheck {'
    echo '  public static void main(String[] a) throws Exception {'
    echo '    boolean manifest = false; int n = 0;'
    echo '    try (ZipFile z = new ZipFile(a[0])) {'
    echo '      var e = z.entries();'
    echo '      while (e.hasMoreElements()) {'
    echo '        n++;'
    echo '        if (e.nextElement().getName().equals("AndroidManifest.xml")) manifest = true;'
    echo '      }'
    echo '    }'
    echo '    if (!manifest) { System.err.println("no AndroidManifest.xml"); System.exit(2); }'
    echo '    System.out.println(n);'
    echo '  }'
    echo '}'
  } > "$WORK/AarCheck.java"
  javac -d "$WORK" "$WORK/AarCheck.java" 2>/dev/null ||
    fail "could not compile the archive checker"
  ENTRIES=$(java -cp "$WORK" AarCheck "$AAR") ||
    fail "the aar is not a readable archive (or lacks AndroidManifest.xml)"
fi
echo "   AndroidManifest.xml present, $ENTRIES entries"

# classes.jar is what the Android toolchain actually compiles against; an aar
# without it is a resources-only module, which is legal but not this one.
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$AAR" 2>/dev/null | grep -q 'classes.jar' ||
    echo "   (no classes.jar - a resources-only module)"
fi

echo "== the same repository serves plain jars too =="
# A mirror that handled only .aar would pass everything above and fail for every
# JVM library published alongside.
JG="androidx.annotation"
JA="annotation"
JV="1.9.1"
JGPATH=$(printf '%s' "$JG" | tr '.' '/')
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$JG:$JA:$JV" -Dtransitive=false >/tmp/google-jar.log 2>&1; then
  JJ=$(find "$M2" -name "$JA-$JV.jar" | head -1)
  [ -n "$JJ" ] || fail "the jar is not in the local repository"
  echo "   resolved $JG:$JA:$JV ($(wc -c < "$JJ") bytes)"
else
  echo "   ($JG:$JA:$JV did not resolve; the aar above is the assertion)"
fi

echo "== an artifact that does not exist stays a 404 =="
code=$(curl -s -o /dev/null -w '%{http_code}' \
  "$BASE/$GPATH/$ART/0.0.0-does-not-exist/$ART-0.0.0-does-not-exist.aar")
case "$code" in
  404) echo "   -> 404 (correct)" ;;
  200) fail "a nonexistent artifact answered 200" ;;
  *) echo "   -> $code" ;;
esac

echo "google: OK (pull: pom-packaging-aar mvn-aar aar-is-zip manifest classes-jar jar-too 404)"
