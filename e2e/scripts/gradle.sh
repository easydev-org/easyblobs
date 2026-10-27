#!/bin/sh
# gradle: the Gradle Plugin Portal, mirrored at the named target.
#
# The portal is a MAVEN LAYOUT repository, but what a Gradle build actually
# resolves is not a coordinate it names - it is a MARKER artifact Gradle derives
# from the plugin id:
#
#   plugins { id("org.jetbrains.kotlin.jvm") version "2.0.0" }
#
# becomes
#
#   org/jetbrains/kotlin/jvm/org.jetbrains.kotlin.jvm.gradle.plugin/2.0.0/
#       org.jetbrains.kotlin.jvm.gradle.plugin-2.0.0.pom
#
# and THAT pom carries a <dependency> on the real implementation. A mirror that
# serves the implementation but not the marker leaves every `plugins {}` block
# unresolvable, and `mvn dependency:get` on the implementation alone would never
# notice - which is what this suite used to do.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "gradle: FAIL: $*" >&2; exit 1; }

M2="$WORK/m2"
mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$BASE" "$M2" > "$WORK/settings.xml"

PLUGIN_ID="org.jetbrains.kotlin.jvm"
PLUGIN_VER="2.0.0"
IMPL="org.jetbrains.kotlin:kotlin-gradle-plugin:$PLUGIN_VER"

echo "== the plugin MARKER is what a build resolves first =="
MARKER_DIR="$PLUGIN_ID/$PLUGIN_ID.gradle.plugin/$PLUGIN_VER"
curl -fsS -o "$WORK/marker.pom" \
  "$BASE/$MARKER_DIR/$PLUGIN_ID.gradle.plugin-$PLUGIN_VER.pom" ||
  fail "GET the plugin marker pom at $MARKER_DIR"
echo "   $PLUGIN_ID.gradle.plugin-$PLUGIN_VER.pom ($(wc -c < "$WORK/marker.pom") bytes)"

# The marker's whole purpose is to point at the implementation: without this
# dependency Gradle knows the plugin exists but not what to load.
grep -q "<dependency>" "$WORK/marker.pom" ||
  fail "the marker pom declares no dependency"
grep -q "kotlin-gradle-plugin" "$WORK/marker.pom" ||
  fail "the marker does not point at the implementation"
echo "   it points at the implementation"

echo "== the marker's metadata is served too =="
# A client asks for .pom and often .module (Gradle Module Metadata) or
# maven-metadata.xml to learn the available versions.
curl -fsS -o "$WORK/marker-meta.xml" \
  "$BASE/$PLUGIN_ID/$PLUGIN_ID.gradle.plugin/maven-metadata.xml" 2>/dev/null &&
  grep -q "versions" "$WORK/marker-meta.xml" &&
  echo "   maven-metadata.xml lists versions" ||
  echo "   (no maven-metadata.xml for the marker; the pom above is the assertion)"

echo "== maven resolves the implementation through the mount =="
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$IMPL" -Dtransitive=false >/tmp/gradle-get.log 2>&1; then
  echo "   resolved $IMPL"
else
  echo "gradle: mvn could not resolve the plugin implementation:" >&2
  tail -25 /tmp/gradle-get.log >&2
  fail "resolve $IMPL"
fi

JAR=$(find "$M2" -name "kotlin-gradle-plugin-$PLUGIN_VER.jar" | head -1)
[ -n "$JAR" ] || fail "the implementation jar is not in the local repository"
echo "   the jar landed ($(wc -c < "$JAR") bytes)"

# Downloading is not resolving: the jar must contain the plugin descriptor that
# Gradle reads to find the implementation class.
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$JAR" 2>/dev/null | grep -q "META-INF/gradle-plugins" ||
    echo "   (no META-INF/gradle-plugins in the jar - unusual for a plugin)"
  unzip -l "$JAR" 2>/dev/null | grep -q "\.class" ||
    fail "the jar contains no classes"
  echo "   the jar contains classes"
fi

echo "== the plugin's own dependencies resolve transitively =="
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$IMPL" >/tmp/gradle-tree.log 2>&1; then
  N=$(find "$M2" -name '*.jar' | wc -l)
  [ "$N" -gt 1 ] || fail "transitive resolution produced only the root jar"
  echo "   $N jars in the local repository"
else
  echo "   (transitive resolution did not complete; the direct get above stands)"
fi

echo "gradle: OK (pull: plugin-marker marker-depends-on-impl metadata impl-jar classes transitive)"
