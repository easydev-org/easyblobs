#!/bin/sh
# Ivy: the upstream host is the first path segment of the mount. sbt (and Ant)
# resolve plugins from an Ivy-layout repository; a legacy sbt plugin lives at
# the extra-attribute path <org>/<module>/scala_2.10/sbt_0.13/<rev>/ivys/ivy.xml.
#
# Assert: the descriptor and its jar both fetch through the mirror, and a
# subsequent fetch is served from the local cache.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
BASE="$EASYBLOBS_BASE/repo.scala-sbt.org/scalasbt/sbt-plugin-releases"
PLUGIN=com.typesafe.sbt/sbt-native-packager/scala_2.10/sbt_0.13/0.7.4
wget -qO /tmp/ivy.xml "$BASE/$PLUGIN/ivys/ivy.xml"
grep -q 'organisation="com.typesafe.sbt"' /tmp/ivy.xml || { echo "ivy: bad descriptor"; exit 1; }

# The jar declared by the descriptor's <publications> must be fetchable. The
# Ivy layout puts it under jars/<artifact>.jar (the artifact name, not the
# versioned file name).
wget -qO /tmp/plugin.jar "$BASE/$PLUGIN/jars/sbt-native-packager.jar"
[ -s /tmp/plugin.jar ] || { echo "ivy: empty jar"; exit 1; }

# Cached path: a repeat fetch must still succeed (served locally).
wget -qO /tmp/ivy2.xml "$BASE/$PLUGIN/ivys/ivy.xml"
cmp -s /tmp/ivy.xml /tmp/ivy2.xml || { echo "ivy: cache mismatch"; exit 1; }

echo "ivy: resolved $PLUGIN ($(wc -c </tmp/plugin.jar) byte jar)"
