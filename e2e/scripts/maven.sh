#!/bin/sh
# maven: the whole client surface against the mount, in both directions.
#
# The adapter is a Maven-layout repository: everything is
# <groupId as path>/<artifactId>/<version>/<file>, plus maven-metadata.xml at
# the artifact and group level, plus .sha1/.md5 beside every file.
#
# PULL - each shape resolves through a different part of the layout:
#   slf4j-api          a plain jar
#   jackson-databind   transitive dependencies (core + annotations must follow)
#   commons-io         a POM with a parent, so the parent must resolve too
#   sources classifier a non-default classifier
#   maven-metadata.xml the version list a client reads before resolving
#   javac + java       the resolved jar must compile and run
#
# HOSTED
#   mvn deploy         a real artefact goes to the mount
#   the layout         jar, pom and maven-metadata.xml are all served
#   a consumer         resolves it back out of the mirror and compiles
#   DELETE             the version stops resolving
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "maven: FAIL: $*" >&2; exit 1; }

# Every resolution goes through the mount and nowhere else.
mkdir -p "$HOME/.m2"
printf '%s\n' \
  '<settings><mirrors><mirror>' \
  '<id>artifact</id><name>artifact maven mirror</name>' \
  "<url>$BASE/</url><mirrorOf>*</mirrorOf>" \
  '</mirror></mirrors></settings>' > "$HOME/.m2/settings.xml"

M2="$WORK/m2"
M() { mvn -q -B -Dmaven.repo.local="$M2" "$@"; }

echo "== the repository root answers =="
curl -fsS "$BASE/archetype-catalog.xml" >/dev/null || fail "archetype-catalog.xml"

echo "== resolve: a plain jar =="
if ! out=$(M dependency:get -Dartifact=org.slf4j:slf4j-api:2.0.9 2>&1); then
  echo "maven: resolve slf4j-api failed:" >&2
  echo "$out" | tail -25 >&2
  fail "resolve slf4j-api"
fi
test -f "$M2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar" || fail "the jar did not land"

echo "== resolve: transitive dependencies =="
M dependency:get -Dartifact=com.fasterxml.jackson.core:jackson-databind:2.16.1 >/dev/null ||
  fail "resolve jackson-databind"
test -f "$M2/com/fasterxml/jackson/core/jackson-core/2.16.1/jackson-core-2.16.1.jar" ||
  fail "jackson-databind's dependency jackson-core was not resolved"
test -f "$M2/com/fasterxml/jackson/core/jackson-annotations/2.16.1/jackson-annotations-2.16.1.jar" ||
  fail "jackson-annotations was not resolved"

echo "== resolve: a POM with a parent =="
M dependency:get -Dartifact=commons-io:commons-io:2.15.1 >/dev/null || fail "resolve commons-io"
test -f "$M2/commons-io/commons-io/2.15.1/commons-io-2.15.1.jar" || fail "commons-io jar"
find "$M2" -name 'commons-parent-*.pom' | grep -q . ||
  fail "commons-io's parent POM was not resolved"

echo "== resolve: a sources classifier =="
M dependency:get -Dartifact=org.slf4j:slf4j-api:2.0.9:jar:sources >/dev/null ||
  fail "resolve the sources classifier"
test -f "$M2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9-sources.jar" ||
  fail "the sources jar did not land"

echo "== maven-metadata.xml lists the version =="
curl -fsS "$BASE/org/slf4j/slf4j-api/maven-metadata.xml" | grep -q '2\.0\.9' ||
  fail "maven-metadata.xml does not list 2.0.9"

echo "== the hash sidecar =="
curl -fsS "$BASE/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar.sha1" >/dev/null ||
  fail "the .sha1 sidecar"

echo "== compile and run against a resolved jar =="
printf '%s\n' \
  'public final class Main {' \
  '  public static void main(String[] a) {' \
  '    System.out.println("java + slf4j: " + org.slf4j.LoggerFactory.class.getName());' \
  '  }' \
  '}' > Main.java
CP="$M2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"
javac -cp "$CP" Main.java || fail "javac against the resolved jar"
java -cp ".:$CP" Main | grep -q 'java + slf4j' || fail "running against the resolved jar"

# ------------------------------------------------------------------ hosted --
G="dev.easyblobs.e2e"
A="ezhello"
V="1.0.0"
PROJ="$WORK/proj"
mkdir -p "$PROJ/src/main/java/dev/easyblobs/e2e"
printf '%s\n' \
  'package dev.easyblobs.e2e;' \
  'public final class Hello {' \
  '  private Hello() {}' \
  '  public static String greet() { return "ez hello"; }' \
  '}' > "$PROJ/src/main/java/dev/easyblobs/e2e/Hello.java"
printf '%s\n' \
  '<project xmlns="http://maven.apache.org/POM/4.0.0">' \
  '  <modelVersion>4.0.0</modelVersion>' \
  "  <groupId>$G</groupId>" \
  "  <artifactId>$A</artifactId>" \
  "  <version>$V</version>" \
  '  <packaging>jar</packaging>' \
  '  <properties>' \
  '    <maven.compiler.source>17</maven.compiler.source>' \
  '    <maven.compiler.target>17</maven.compiler.target>' \
  '    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>' \
  '  </properties>' \
  '  <distributionManagement>' \
  "    <repository><id>artifact</id><url>$BASE/</url></repository>" \
  '  </distributionManagement>' \
  '</project>' > "$PROJ/pom.xml"

echo "== mvn deploy =="
( cd "$PROJ" && M deploy >/dev/null 2>&1 ) || fail "mvn deploy"

echo "== the layout on the mount =="
curl -fsS "$BASE/$G/$A/$V/$A-$V.jar" >/dev/null || fail "the deployed jar is not served"
curl -fsS "$BASE/$G/$A/$V/$A-$V.pom" >/dev/null || fail "the deployed pom is not served"
curl -fsS "$BASE/$G/$A/maven-metadata.xml" | grep -q "$V" ||
  fail "the artifact maven-metadata.xml does not list $V"

echo "== a consumer resolves it back =="
CONS="$WORK/consumer"
mkdir -p "$CONS/src/main/java"
printf '%s\n' \
  '<project xmlns="http://maven.apache.org/POM/4.0.0">' \
  '  <modelVersion>4.0.0</modelVersion>' \
  '  <groupId>dev.easyblobs.e2e</groupId>' \
  '  <artifactId>consumer</artifactId>' \
  '  <version>1</version>' \
  '  <properties><maven.compiler.source>17</maven.compiler.source>' \
  '  <maven.compiler.target>17</maven.compiler.target></properties>' \
  '  <dependencies><dependency>' \
  "    <groupId>$G</groupId><artifactId>$A</artifactId><version>$V</version>" \
  '  </dependency></dependencies>' \
  '</project>' > "$CONS/pom.xml"
printf '%s\n' \
  'public final class Uses {' \
  '  public static void main(String[] a) {' \
  '    System.out.println(dev.easyblobs.e2e.Hello.greet());' \
  '  }' \
  '}' > "$CONS/src/main/java/Uses.java"
( cd "$CONS" && M compile >/dev/null 2>&1 ) ||
  fail "a consumer could not compile against the deployed artefact"
echo "   the consumer compiled against it"

echo "== DELETE the version =="
# Maven's own protocol has no DELETE; this is the mount's extension.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/$G/$A/$V/$A-$V.jar")
case "$code" in 200|202|204) ;; *) fail "DELETE the jar -> $code" ;; esac
if curl -fsS "$BASE/$G/$A/$V/$A-$V.jar" >/dev/null 2>&1; then
  fail "the deleted jar still resolves"
fi
echo "   the jar is gone"

echo "maven: OK (pull: resolve[plain transitive parent classifier sources] metadata hash compile+run)"
echo "maven: OK (hosted: deploy layout metadata consumer-compiles delete)"
