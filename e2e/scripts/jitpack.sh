#!/bin/sh
# JitPack: a Maven-layout mirror at the named target mount. JitPack only serves
# artifacts it has built; master-SNAPSHOT for a known repo works.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/jitpack.pom \
  "$EASYBLOBS_BASE/com/github/jitpack/maven-simple/master-SNAPSHOT/maven-simple-master-SNAPSHOT.pom"
grep -q "maven-simple" /tmp/jitpack.pom || { echo "jitpack: bad pom"; exit 1; }
echo "jitpack: maven-simple master-SNAPSHOT pom ok"
