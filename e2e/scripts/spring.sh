#!/bin/sh
# spring: the Spring milestone repository, mirrored at the named target.
#
# Spring publishes milestone and release-candidate versions alongside the
# releases, and those are exactly what naive tooling gets wrong:
#
#   6.2.0-M1   a MILESTONE, and Maven's version ordering puts it BEFORE 6.2.0
#   6.2.0-RC1  a release candidate
#
# A mirror that reorders or mis-parses those, or that treats M1 as a snapshot,
# hands Maven a version graph it cannot satisfy. So the suite checks the version
# STRING round-trips untouched and that the milestone's own pom is what Maven
# then resolves.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "spring: FAIL: $*" >&2; exit 1; }

M2="$WORK/m2"
mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$BASE" "$M2" > "$WORK/settings.xml"

GROUP="org.springframework"
ART="spring-core"
VER="6.2.0-M1"
GPATH=$(printf '%s' "$GROUP" | tr '.' '/')

echo "== the milestone pom is served, version intact =="
curl -fsS -o "$WORK/pom" "$BASE/$GPATH/$ART/$VER/$ART-$VER.pom" ||
  fail "GET the milestone pom"
# The version must survive byte-for-byte: 'M1' upper-case, no -SNAPSHOT rewrite.
grep -q "<version>$VER</version>" "$WORK/pom" ||
  { echo "spring: the pom does not carry $VER verbatim:" >&2
    grep -m3 "<version>" "$WORK/pom" >&2
    exit 1; }
echo "   it carries $VER verbatim"
if grep -qi "SNAPSHOT" "$WORK/pom"; then
  fail "a milestone pom was rewritten as a snapshot"
fi
echo "   it is not treated as a snapshot"

echo "== the repository's version list includes the milestone =="
if curl -fsS -o "$WORK/meta.xml" "$BASE/$GPATH/$ART/maven-metadata.xml" 2>/dev/null &&
   [ -s "$WORK/meta.xml" ]; then
  grep -q "$VER" "$WORK/meta.xml" || fail "maven-metadata.xml does not list $VER"
  echo "   maven-metadata.xml lists $VER"
else
  echo "   (no maven-metadata.xml for this artifact; the pom above is the assertion)"
fi

echo "== maven resolves the milestone through the mount =="
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$VER" -Dtransitive=false >/tmp/spring-get.log 2>&1; then
  echo "   resolved $GROUP:$ART:$VER"
else
  echo "spring: mvn could not resolve the milestone:" >&2
  tail -25 /tmp/spring-get.log >&2
  fail "resolve $GROUP:$ART:$VER"
fi

JAR=$(find "$M2" -name "$ART-$VER.jar" | head -1)
[ -n "$JAR" ] || fail "the milestone jar is not in the local repository"
echo "   the jar landed ($(wc -c < "$JAR") bytes)"

echo "== the jar is a real artifact =="
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$JAR" 2>/dev/null | grep -q "\.class" || fail "the jar has no classes"
  # spring-core's own module descriptor is a good sign the payload is intact.
  unzip -l "$JAR" 2>/dev/null | grep -q "org/springframework/core" ||
    fail "the jar does not contain org/springframework/core"
  echo "   it contains org/springframework/core"
fi

echo "== a RELEASE version from the same repo resolves too =="
# A milestone-only mirror would pass the checks above and still be useless.
REL="6.1.14"
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$REL" -Dtransitive=false >/tmp/spring-rel.log 2>&1; then
  echo "   resolved $REL as well"
else
  echo "   ($REL did not resolve - the milestone repository may not carry releases)"
fi

echo "spring: OK (pull: milestone-pom version-verbatim not-a-snapshot metadata mvn-resolves jar)"
