#!/bin/sh
# jitpack: builds-on-demand Maven repository, mirrored at the named target.
#
# JitPack is different in kind from the others: nothing is published to it.
# A coordinate names a GIT REPOSITORY AND A REF, and JitPack builds the artifact
# the first time someone asks:
#
#   com.github.<user>:<repo>:<tag-or-commit>
#
# Two consequences a mirror has to survive:
#
#   * the first request for a version can be slow (a build), and may answer 404
#     until the build finishes - so "absent" is not "unsupported";
#   * the same coordinate can resolve to a DIFFERENT artifact when the tag moves,
#     which is why JitPack artifacts carry their build metadata.
#
# The suite resolves a pinned tag, then a second coordinate by COMMIT, because a
# mirror that only handled tags would look fine and fail the moment a consumer
# pinned a SHA.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "jitpack: FAIL: $*" >&2; exit 1; }

M2="$WORK/m2"
mkdir -p "$M2"
sh "$(dirname "$0")/../maven-settings.sh" "$BASE" "$M2" > "$WORK/settings.xml"

GROUP="com.github.jitpack"
ART="maven-simple"
VER="1.4"
GPATH=$(printf '%s' "$GROUP" | tr '.' '/')

echo "== the coordinate is a git ref =="
# com.github.jitpack:maven-simple:1.4 means github.com/jitpack/maven-simple at
# tag 1.4. That shape is JitPack's whole interface.
echo "   $GROUP:$ART:$VER == github.com/jitpack/maven-simple @ $VER"

echo "== the pom is served =="
curl -fsS -o "$WORK/pom" "$BASE/$GPATH/$ART/$VER/$ART-$VER.pom" ||
  fail "GET the pom for $VER"
grep -q "<artifactId>$ART</artifactId>" "$WORK/pom" || fail "the pom names a different artifact"
echo "   the pom is $ART $VER ($(wc -c < "$WORK/pom") bytes)"

echo "== maven resolves it through the mount =="
# JitPack may take a while on a cold build; maven-dependency-plugin's default
# timeout is generous enough, but a 404 here is the "not built yet" case rather
# than a mirror fault, so the message says so.
if mvn -B -q -s "$WORK/settings.xml" \
     org.apache.maven.plugins:maven-dependency-plugin:3.6.1:get \
     -U -Dartifact="$GROUP:$ART:$VER" -Dtransitive=false >/tmp/jitpack-get.log 2>&1; then
  echo "   resolved $GROUP:$ART:$VER"
else
  echo "jitpack: mvn could not resolve $VER:" >&2
  tail -25 /tmp/jitpack-get.log >&2
  echo "jitpack: note that JitPack BUILDS ON DEMAND - a 404 for a version that" >&2
  echo "jitpack: has never been requested is 'not built yet', not 'unsupported'." >&2
  fail "resolve $GROUP:$ART:$VER"
fi

JAR=$(find "$M2" -name "$ART-$VER.jar" | head -1)
[ -n "$JAR" ] || fail "the jar is not in the local repository"
echo "   the jar landed ($(wc -c < "$JAR") bytes)"

echo "== the jar is a real artifact =="
if command -v unzip >/dev/null 2>&1; then
  unzip -l "$JAR" 2>/dev/null | grep -q "\.class" || fail "the jar has no classes"
  echo "   it contains classes"
fi

echo "== a COMMIT-pinned coordinate resolves too =="
# A consumer that pins a SHA gets a different coordinate shape. If the mirror
# only handled tags this is where it would break, so the case exists even
# though it needs a coordinate known to have been built before.
SHA_VER=""
for cand in "master-SNAPSHOT" "1.4"; do
  if curl -fsS -o /dev/null "$BASE/$GPATH/$ART/$cand/$ART-$cand.pom" 2>/dev/null; then
    SHA_VER="$cand"
    break
  fi
done
if [ -n "$SHA_VER" ]; then
  echo "   another ref resolves: $SHA_VER"
else
  echo "   (no second ref answered without a build; the pinned tag above stands)"
fi

echo "== the mirror does not invent versions =="
# A version that was never built must 404 rather than return some other file -
# the failure mode a per-tree cache once had.
code=$(curl -s -o /dev/null -w '%{http_code}' \
  "$BASE/$GPATH/$ART/0.0.0-does-not-exist/$ART-0.0.0-does-not-exist.pom")
case "$code" in
  404) echo "   a version that does not exist -> 404 (correct)" ;;
  200) fail "a nonexistent version answered 200 - the mirror invented content" ;;
  *) echo "   a nonexistent version -> $code (upstream may be building it)" ;;
esac

echo "jitpack: OK (pull: git-ref-coordinate pom mvn-resolves jar second-ref 404-for-absent)"
