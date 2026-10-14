#!/bin/sh
# Google Maven (Android): a Maven-layout mirror at the named target mount.
# Fetch an Android artifact POM + AAR.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/gradle.pom "$EASYBLOBS_BASE/com/android/tools/build/gradle/8.7.0/gradle-8.7.0.pom"
grep -q "<artifactId>gradle</artifactId>" /tmp/gradle.pom || { echo "google: bad pom"; exit 1; }
wget -qO /tmp/core.aar "$EASYBLOBS_BASE/androidx/core/core/1.13.0/core-1.13.0.aar"
[ -s /tmp/core.aar ] || { echo "google: empty aar"; exit 1; }
echo "google: gradle pom + androidx core aar ok"
