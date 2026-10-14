#!/bin/sh
# Spring repo: a Maven-layout mirror at the named target mount. The /release
# path requires auth (401); the /milestone repo is public (the target's base),
# so exercise that path.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/spring.pom \
  "$EASYBLOBS_BASE/org/springframework/spring-core/6.2.0-M1/spring-core-6.2.0-M1.pom"
grep -q "<artifactId>spring-core</artifactId>" /tmp/spring.pom || { echo "spring: bad pom"; exit 1; }
echo "spring: milestone spring-core pom ok"
