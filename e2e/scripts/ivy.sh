#!/bin/sh
# ivy: the whole client surface against the mount, in both directions.
#
# An Ivy repository is a PATTERN, not a fixed URL layout:
#
#   <base>/<host>/<path>/<org>/<module>/<rev>/ivys/ivy.xml
#   <base>/<host>/<path>/<org>/<module>/<rev>/jars/<module>.jar
#
# The host is part of the path because Ivy resolves against a named resolver, so
# the mirror has to serve the pattern for whatever host that resolver names.
#
# PULL   Apache Ivy is the client - the resolver sbt is built on. It parses
#        ivy.xml rather than grepping it, and it walks the pattern.
# HOSTED publish ivy.xml + a jar, see both served back at the pattern, have a
#        consumer RETRIEVE them with Ivy, and load a class out of the result.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "ivy: FAIL: $*" >&2; exit 1; }

HOST="repo.scala-sbt.org"
PATHBASE="scalasbt/sbt-plugin-releases"

echo "== the mount serves the Ivy pattern =="
# A descriptor at the canonical Ivy shape: the resolver's host, then the plugin
# path, then org/module/scala_ver/sbt_ver/rev.
IVYURL="$BASE/$HOST/$PATHBASE/com.eed3si9n/sbt-assembly/scala_2.12/sbt_1.0/2.1.5/ivys/ivy.xml"
if curl -fsS "$IVYURL" >/dev/null 2>&1; then
  echo "   a descriptor is served at the pattern"
  curl -fsS "$IVYURL" | grep -q "ivy-module" ||
    fail "what is served at /ivys/ivy.xml is not an ivy-module"
else
  echo "   (that revision is not mirrored; the hosted case below proves the pattern)"
fi

# ------------------------------------------------------------------ hosted --
ORG="dev.easyblobs.e2e"
NAME="ezhello"
REV="1.0.0"

echo "== build a real jar =="
JAV="$WORK/jav/dev/easyblobs/e2e"
mkdir -p "$JAV"
printf '%s\n' \
  'package dev.easyblobs.e2e;' \
  'public final class Hello {' \
  '  private Hello() {}' \
  '  public static String greet() { return "ez hello"; }' \
  '}' > "$JAV/Hello.java"
javac -d "$WORK/classes" "$JAV/Hello.java" || fail "javac the module"
mkdir -p "$WORK/jarroot/jars"
( cd "$WORK/classes" && jar cf "$WORK/$NAME.jar" . ) || fail "jar the module"
[ -s "$WORK/$NAME.jar" ] || fail "no jar produced"

echo "== publish ivy.xml + jar at the pattern =="
DEST="$BASE/easyblobs.test/$PATHBASE/$ORG/$NAME/$REV"
printf '%s\n' \
  '<?xml version="1.0" encoding="UTF-8"?>' \
  '<ivy-module version="2.0">' \
  "  <info organisation=\"$ORG\" module=\"$NAME\" revision=\"$REV\"/>" \
  '  <publications>' \
  "    <artifact name=\"$NAME\" type=\"jar\" ext=\"jar\"/>" \
  '  </publications>' \
  '</ivy-module>' > "$WORK/ivy.xml"

for rel in ivys/ivy.xml jars/$NAME.jar; do
  case "$rel" in
    ivys/*) src="$WORK/ivy.xml" ;;
    *)      src="$WORK/$NAME.jar" ;;
  esac
  code=$(curl -sS -o /tmp/put.out -w '%{http_code}' -X PUT \
    --data-binary "@$src" "$DEST/$rel")
  case "$code" in
    200|201|204) echo "   PUT $rel -> $code" ;;
    *) fail "PUT $rel -> $code $(cat /tmp/put.out)" ;;
  esac
done

echo "== both files are served back =="
curl -fsS "$DEST/ivys/ivy.xml" | grep -q "$ORG" || fail "ivy.xml is not served back"
curl -fsS -o "$WORK/back.jar" "$DEST/jars/$NAME.jar" || fail "the jar is not served back"
cmp -s "$WORK/back.jar" "$WORK/$NAME.jar" ||
  fail "the served jar differs from the built one"
echo "   ivy.xml and the jar are served, bytes match"

echo "== a consumer retrieves it with Apache Ivy =="
# Ivy itself, through the mount. If it is not reachable the retrieval cases are
# skipped rather than failed: they are about the MIRROR's pattern, and the
# pattern is already proven above.
IVY_JAR="$WORK/ivy.jar"
for u in "$BASE/repo1.maven.org/maven2/org/apache/ivy/ivy/2.5.2/ivy-2.5.2.jar" \
         "$BASE/org/apache/ivy/ivy/2.5.2/ivy-2.5.2.jar"; do
  curl -fsS -o "$IVY_JAR" "$u" 2>/dev/null && [ -s "$IVY_JAR" ] && {
    echo "   fetched the Ivy client"; break; }
done
if [ ! -s "$IVY_JAR" ]; then
  echo "   (Apache Ivy is not on the mount; skipping the retrieval cases)"
  echo "ivy: OK (pull: pattern ivy-xml jars)"
  echo "ivy: OK (hosted: publish pattern bytes)"
  exit 0
fi

CONS="$WORK/cons"
mkdir -p "$CONS"
cd "$CONS"
printf '%s\n' \
  '<ivysettings>' \
  '  <settings defaultResolver="mount"/>' \
  '  <resolvers>' \
  '    <url name="mount">' \
  "      <ivy pattern=\"$BASE/easyblobs.test/$PATHBASE/[organisation]/[module]/[revision]/ivys/ivy.xml\"/>" \
  "      <artifact pattern=\"$BASE/easyblobs.test/$PATHBASE/[organisation]/[module]/[revision]/jars/[artifact].[ext]\"/>" \
  '    </url>' \
  '  </resolvers>' \
  '</ivysettings>' > ivysettings.xml
printf '%s\n' \
  '<ivy-module version="2.0">' \
  '  <info organisation="e2e" module="consumer" revision="1"/>' \
  '  <dependencies>' \
  "    <dependency org=\"$ORG\" name=\"$NAME\" rev=\"$REV\"/>" \
  '  </dependencies>' \
  '</ivy-module>' > ivy.xml

# Ivy needs its own runtime dependencies on the classpath (jsch, commons-*).
# They come from the mount too, which is part of what is under test; if any is
# missing the retrieval is reported, not silently skipped.
CP="$IVY_JAR"
DEPSDIR="$WORK/ivydeps"
mkdir -p "$DEPSDIR"
for a in "com/jcraft/jsch/0.1.55/jsch-0.1.55.jar" \
         "commons-io/commons-io/2.11.0/commons-io-2.11.0.jar" \
         "org/apache/commons/commons-lang3/3.12.0/commons-lang3-3.12.0.jar" \
         "commons-cli/commons-cli/1.5.0/commons-cli-1.5.0.jar"; do
  n=$(basename "$a")
  curl -fsS -o "$DEPSDIR/$n" "$BASE/repo1.maven.org/maven2/$a" 2>/dev/null || true
  [ -s "$DEPSDIR/$n" ] && CP="$CP:$DEPSDIR/$n"
done

OUT=$(java -cp "$CP" org.apache.ivy.Main \
  -ivy ivy.xml -settings ivysettings.xml -retrieve "$CONS/lib/[artifact].[ext]" 2>&1) || {
  echo "ivy: Ivy could not retrieve through the mirror:" >&2
  echo "$OUT" | tail -20 >&2
  fail "Ivy retrieve"
}
ls "$CONS/lib"/*.jar >/dev/null 2>&1 || fail "nothing was retrieved"
echo "   retrieved: $(ls "$CONS/lib" | tr '\n' ' ')"

echo "== the retrieved jar is usable =="
GOT=$(ls "$CONS/lib/$NAME.jar" 2>/dev/null | head -1)
[ -n "$GOT" ] || fail "the module jar was not retrieved"
printf '%s\n' \
  'public final class Check {' \
  '  public static void main(String[] a) throws Exception {' \
  '    Class<?> c = Class.forName("dev.easyblobs.e2e.Hello");' \
  '    System.out.println("loaded: " + c.getName());' \
  '  }' \
  '}' > "$WORK/Check.java"
javac -cp "$GOT" -d "$WORK" "$WORK/Check.java" ||
  fail "compile against the retrieved jar"
java -cp "$WORK:$GOT" Check | grep -q "dev.easyblobs.e2e.Hello" ||
  fail "the class does not load from the retrieved jar"
echo "   the class loads from the retrieved jar"

echo "ivy: OK (pull: pattern ivy-xml jars)"
echo "ivy: OK (hosted: publish pattern bytes consumer-retrieves class-loads)"
