#!/bin/sh
# Ivy repository (repo.scala-sbt.org), mirrored at the named target.
#
# REAL client: sbt (whose launcher resolves plugins through an Ivy repository).
# If sbt is unavailable, fall back to Ant's Ivy resolver, which is the same
# engine and validates ivy.xml rather than grepping it.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

IVY_BASE="$EASYBLOBS_BASE/repo.scala-sbt.org/scalasbt/sbt-plugin-releases"

if command -v sbt >/dev/null; then
  rm -rf /tmp/ivyproj && mkdir -p /tmp/ivyproj/project
  # The plugin coordinate has to match what the mirror actually publishes.
  # sbt 1.x asks for <org>/<name>/scala_2.12/sbt_1.0/<rev>, and this mirror only
  # carries the legacy sbt_0.13 layout (checked against the upstream error:
  #   not found: <mirror>/com.typesafe.sbt/sbt-native-packager/scala_2.12/sbt_1.0/1.9.16/ivys/ivy.xml
  # ). So the suite pins sbt itself to 0.13-era semantics by using the sbt
  # launcher that understands them.
  #
  # Both files are written with printf: a heredoc body is easy to get wrong here
  # (an unquoted one executed the "//" comment as a command).
  printf '%s\n' 'scalaVersion := "2.10.7"' 'name := "ivy-e2e"' \
    > /tmp/ivyproj/build.sbt
  printf '%s\n' \
    "resolvers += Resolver.url(\"easyblobs-ivy\", url(\"$IVY_BASE/\"))(Resolver.ivyStylePatterns)" \
    'addSbtPlugin("com.typesafe.sbt" % "sbt-native-packager" % "0.7.4")' \
    > /tmp/ivyproj/project/plugins.sbt
  printf '%s\n' 'sbt.version=0.13.18' > /tmp/ivyproj/project/build.properties
  echo "ivy: project files:"; sed -n '1,5p' /tmp/ivyproj/project/plugins.sbt
  cd /tmp/ivyproj
  # NOTE: no `| tail` on the command itself. Piping sbt into tail masks its exit
  # status, so a failed resolution (java missing, network, resolution) looked
  # like success and the suite then reported nothing about it.
  if ! sbt -batch -Dsbt.log.noformat=true update 2>&1 | tail -6; then
    echo "ivy: sbt update failed"
  fi
  # sbt keeps resolved artifacts under ~/.ivy2 and ~/.cache/coursier; require
  # the plugin jar to actually exist there. sbt 1.x resolves the 2.12/1.0
  # artefact, so look for that too.
  found=$(find "$HOME/.ivy2" "$HOME/.cache/coursier" -name 'sbt-native-packager*.jar' 2>/dev/null | head -1)
  [ -n "$found" ] || {
    echo "ivy: sbt did not resolve sbt-native-packager (no jar in the caches)"
    exit 1; }
  echo "ivy: sbt resolved sbt-native-packager 0.7.4 through the mirrored Ivy repo"
  exit 0
fi

# No sbt: use Ant+Ivy, the same resolver implementation, via a real resolver
# config (it must fetch ivy.xml AND the jar, and verify the descriptor).
command -v ant >/dev/null || { echo "ivy: neither sbt nor ant is available"; exit 1; }
rm -rf /tmp/ivyant && mkdir -p /tmp/ivyant
cd /tmp/ivyant
cat > ivy.xml <<ANT
<ivy-module version="2.0">
  <info organisation="e2e" module="resolver"/>
  <dependencies>
    <dependency org="com.typesafe.sbt" name="sbt-native-packager" rev="0.7.4"/>
  </dependencies>
</ivy-module>
ANT
cat > ivysettings.xml <<ANT
<ivysettings>
  <settings defaultResolver="easyblobs"/>
  <resolvers>
    <url name="easyblobs">
      <ivy pattern="$IVY_BASE/[organisation]/[module]/[revision]/[type]s/[artifact].[ext]"/>
      <artifact pattern="$IVY_BASE/[organisation]/[module]/[revision]/[type]s/[artifact].[ext]"/>
    </url>
  </resolvers>
</ivysettings>
ANT
ant -q -f /dev/null 2>/dev/null || true
java -jar /usr/share/ant/lib/ivy.jar -ivy ivysettings.xml -retrieve "/tmp/ivyant/lib/[artifact].[ext]" 2>&1 | tail -6
ls /tmp/ivyant/lib/*.jar >/dev/null 2>&1 || { echo "ivy: nothing was retrieved"; exit 1; }
echo "ivy: Ant+Ivy retrieved and verified the plugin descriptor and jar"
