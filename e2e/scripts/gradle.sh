#!/bin/sh
# Gradle Plugin Portal: a Maven-layout mirror at the named target mount.
# Fetch a plugin marker POM.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/kotlin.pom \
  "$EASYBLOBS_BASE/org/jetbrains/kotlin/kotlin-stdlib/2.0.0/kotlin-stdlib-2.0.0.pom"
grep -q "<artifactId>kotlin-stdlib</artifactId>" /tmp/kotlin.pom || { echo "gradle: bad pom"; exit 1; }
echo "gradle: plugin portal kotlin-stdlib pom ok"
