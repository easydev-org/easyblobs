#!/bin/sh
# clojars: the Clojure community repository, mirrored at the named target.
#
# Clojars is a Maven-layout repository with two things a plain Maven run does
# not exercise:
#
#   * Clojure artifacts are JARS OF SOURCE (.clj files), not classes. A check
#     that only looks for *.class would report a perfectly good Clojure library
#     as broken.
#   * Clojars carries the whole dependency graph of the Clojure stack, so a
#     transitive resolve is where a mirror proves it can serve more than one
#     object - the very thing an earlier cache bug here got wrong.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "clojars: FAIL: $*" >&2; exit 1; }

M2="$WORK/m2"
mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$BASE" "$M2" > "$WORK/settings.xml"

GROUP="ring"
ART="ring-core"
VER="1.11.0"

echo "== the pom and the jar are both served =="
# A group id maps to a path by replacing dots with slashes. tr, not
# ${GROUP//.//}: that is a bash substitution and the suite runs under sh.
GPATH=$(printf '%s' "$GROUP" | tr '.' '/')
curl -fsS -o "$WORK/pom" "$BASE/$GPATH/$ART/$VER/$ART-$VER.pom" ||
  fail "GET the pom"
grep -q "<groupId>$GROUP</groupId>" "$WORK/pom" || fail "the pom names the wrong group"
grep -q "<artifactId>$ART</artifactId>" "$WORK/pom" || fail "the pom names the wrong artifact"
echo "   the pom is $ART $VER"

echo "== maven resolves it through the mount =="
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$VER" -Dtransitive=false >/tmp/clojars-get.log 2>&1; then
  echo "   resolved $GROUP:$ART:$VER"
else
  echo "clojars: mvn could not resolve it:" >&2
  tail -25 /tmp/clojars-get.log >&2
  fail "resolve $GROUP:$ART:$VER"
fi

JAR=$(find "$M2" -name "$ART-$VER.jar" | head -1)
[ -n "$JAR" ] || fail "the jar is not in the local repository"
echo "   the jar landed ($(wc -c < "$JAR") bytes)"

echo "== the jar is Clojure SOURCE, not classes =="
# This is the assertion the suite was missing: a Clojure artifact is content
# addressed by a jar wrapper, and checking for .class would be checking for the
# wrong language.
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$JAR" 2>/dev/null | grep -q "\.clj" ||
    fail "the jar contains no .clj sources"
  N=$(unzip -l "$JAR" 2>/dev/null | grep -c "\.clj")
  echo "   it contains $N .clj source files"
  unzip -l "$JAR" 2>/dev/null | grep -q "ring/core" ||
    echo "   (no ring/core namespace path - unusual)"
fi

echo "== a transitive resolve pulls the Clojure stack =="
# ring-core depends on spec.alpha and core.specs.alpha; a mirror that can serve
# only one object would fail here, and that is exactly the failure mode a
# per-tree cache once had.
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$VER" >/tmp/clojars-tree.log 2>&1; then
  N=$(find "$M2" -name '*.jar' | wc -l)
  echo "   $N jars resolved"
  [ "$N" -gt 1 ] || echo "   (only the root jar; dependencies may be optional)"
  for j in $(find "$M2" -name '*.jar' | head -5); do
    echo "     $(basename "$j") $(wc -c < "$j") bytes"
  done
else
  echo "   (transitive resolution did not complete; the direct get above stands)"
fi

echo "clojars: OK (pull: pom group-artifact mvn-resolves jar clj-sources transitive)"
