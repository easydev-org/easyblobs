#!/bin/sh
# Clojars: a Maven-layout mirror at the named target mount. Fetch a Clojure
# artifact POM + jar.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/clj.pom "$EASYBLOBS_BASE/ring/ring-core/1.11.0/ring-core-1.11.0.pom"
grep -q "<artifactId>ring-core</artifactId>" /tmp/clj.pom || { echo "clojars: bad pom"; exit 1; }
wget -qO /tmp/clj.jar "$EASYBLOBS_BASE/ring/ring-core/1.11.0/ring-core-1.11.0.jar"
[ -s /tmp/clj.jar ] || { echo "clojars: empty jar"; exit 1; }
echo "clojars: ring-core pom + jar ok"
