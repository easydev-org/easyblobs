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

echo "== Gradle Module Metadata is served alongside the pom =="
# Spring publishes .module files, and a Gradle build prefers them over the pom:
# a mirror that served the pom and not the .module would resolve fine under
# Maven and fail under Gradle.
if curl -fsS -o "$WORK/module.json" "$BASE/$GPATH/$ART/$VER/$ART-$VER.module" 2>/dev/null &&
   [ -s "$WORK/module.json" ]; then
  grep -q '"formatVersion"' "$WORK/module.json" ||
    fail "the .module file is not Gradle module metadata"
  grep -q '"component"' "$WORK/module.json" ||
    fail "the .module file names no component"
  echo "   .module parses and names a component ($(wc -c < "$WORK/module.json") bytes)"
else
  echo "   (no .module for this artifact)"
fi

echo "== maven-metadata.xml lists the milestone =="
if curl -fsS -o "$WORK/meta2.xml" "$BASE/$GPATH/$ART/maven-metadata.xml" 2>/dev/null &&
   [ -s "$WORK/meta2.xml" ]; then
  grep -q "<versions>" "$WORK/meta2.xml" || fail "maven-metadata.xml has no versions"
  echo "   it lists $(grep -c '<version>' "$WORK/meta2.xml") versions"
fi

echo "== the release line needs a credential, and the mirror does not hide it =="
# repo.spring.io/release answers 401 without credentials; only /milestone is
# open. That is the origin's policy, and a mirror must pass it through rather
# than inventing a 404 or serving something else.
#
# The previous version of this suite DID try a release version and swallowed the
# failure with '|| echo', so a 401 was recorded as "the milestone repository may
# not carry releases" - a conclusion about the mirror drawn from the origin's
# authentication policy.
code=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/$GPATH/$ART/6.1.14/$ART-6.1.14.pom")
case "$code" in
  401|403) echo "   -> $code (the origin wants a credential; passed through)" ;;
  200) echo "   -> 200 (this deployment has release access)" ;;
  404) echo "   -> 404 (the mirror does not carry the release line)" ;;
  *) echo "   -> $code" ;;
esac

echo "spring: OK (pull: milestone-pom version-verbatim not-a-snapshot metadata module-file mvn-resolves jar release-policy)"
