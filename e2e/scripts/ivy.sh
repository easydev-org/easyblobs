#!/bin/sh
# Ivy repository (repo.scala-sbt.org/sbt-plugin-releases), mirrored at the mount.
#
# REAL client: Apache Ivy - the resolver sbt itself is built on. It is the right
# tool here because the mirror carries the LEGACY Ivy layout
# (<org>/<module>/scala_2.10/sbt_0.13/<rev>/ivys/ivy.xml + jars/), which modern
# sbt no longer asks for:
#
#   sbt 1.x   requests .../scala_2.12/sbt_1.0/<rev>/ivys/ivy.xml  -> 404
#   sbt 0.13  cannot even start on Java 21:
#             [launcher] UnsupportedOperationException: The Security Manager is
#             deprecated and will be removed in a future release
#
# Ivy has neither problem: it resolves exactly the pattern it is given, and it
# parses the descriptor (dependency/production metadata) rather than grepping
# the XML.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

IVY_BASE="$EASYBLOBS_BASE/repo.scala-sbt.org/scalasbt/sbt-plugin-releases"
PLUGIN_ORG=com.typesafe.sbt
PLUGIN_NAME=sbt-native-packager
PLUGIN_REV=0.7.4

# Fetch the Ivy jar: it is the client, so the harness must provide it.
IVY_JAR=/opt/ivy/ivy.jar
if [ ! -s "$IVY_JAR" ]; then
  mkdir -p /opt/ivy
  wget -q -O "$IVY_JAR" \
    "https://repo1.maven.org/maven2/org/apache/ivy/ivy/2.5.2/ivy-2.5.2.jar"
fi
[ -s "$IVY_JAR" ] || { echo "ivy: could not obtain the Apache Ivy jar"; exit 1; }
command -v java >/dev/null || { echo "ivy: java is missing"; exit 1; }

rm -rf /tmp/ivywork && mkdir -p /tmp/ivywork
cd /tmp/ivywork

# The module we resolve: a real dependency on the mirrored plugin.
# Ivy validates both documents against an XSD, so the namespace declaration is
# required - without it Ivy aborts with
#   cvc-elt.1.a: Cannot find the declaration of element 'ivysettings'
cat > ivy.xml <<XML
<ivy-module version="2.0"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:noNamespaceSchemaLocation="https://ant.apache.org/ivy/schemas/ivy.xsd">
  <info organisation="e2e" module="ivy-e2e"/>
  <dependencies>
    <dependency org="$PLUGIN_ORG" name="$PLUGIN_NAME" rev="$PLUGIN_REV"
                conf="default->default"/>
  </dependencies>
</ivy-module>
XML

# Resolve ONLY against the mount: one URL resolver, no chain to Maven Central.
#
# The patterns must be written in terms of Ivy's TOKEN variables
# ([organisation]/[module]/[revision]/[type]/[artifact]/[ext]) - not with the
# values baked in. Baking them in made Ivy fail with
#   Cannot invoke "ModuleRevisionId.getModuleId()" because "mrid" is null
# because a pattern with no tokens cannot be turned into a lookup.
cat > ivysettings.xml <<XML
<ivysettings
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:noNamespaceSchemaLocation="https://ant.apache.org/ivy/schemas/ivysettings.xsd">
  <settings defaultResolver="easyblobs"/>
  <resolvers>
    <url name="easyblobs" m2compatible="false">
      <ivy pattern="$IVY_BASE/[organisation]/[module]/scala_2.10/sbt_0.13/[revision]/ivys/ivy.xml"/>
      <artifact pattern="$IVY_BASE/[organisation]/[module]/scala_2.10/sbt_0.13/[revision]/[type]s/[artifact].[ext]"/>
    </url>
  </resolvers>
</ivysettings>
XML

echo "ivy: resolving $PLUGIN_ORG:$PLUGIN_NAME:$PLUGIN_REV through the mount"
# NOTE: only real Ivy options here. An unrecognised flag makes Ivy print its
# help text and exit 0 without resolving anything - which is exactly what
# '-noillustrations' did in the previous run.
#
# -settings, NOT -ivy.
#
# -ivy names a MODULE DESCRIPTOR (ivy.xml), so passing ivysettings.xml there
# makes Ivy parse a settings document as a module - which is exactly the
# NullPointerException it kept reporting:
#   Cannot invoke "ModuleRevisionId.getModuleId()" because "mrid" is null
#
# -novalidate: Ivy otherwise fetches the XSD over the network and a cold runner
# fails with 'cvc-elt.1.a: Cannot find the declaration of element ivysettings'.
if ! java -jar "$IVY_JAR" \
      -settings ivysettings.xml \
      -ivy ivy.xml \
      -novalidate \
      -retrieve '/tmp/ivywork/lib/[artifact].[ext]' 2>&1 | tail -20; then
  echo "ivy: the resolver failed"
fi

# Ivy exits 0 even when it reports "not resolved" in some versions, so assert
# the artifact it was asked for is actually on disk.
jar=$(find /tmp/ivywork/lib -name "$PLUGIN_NAME*.jar" 2>/dev/null | head -1)
[ -n "$jar" ] || {
  echo "ivy: the plugin jar was not retrieved through the mirror"; exit 1; }
# And that it is a real jar, not an error page.
java -cp "$jar" -version >/dev/null 2>&1 || \
  unzip -l "$jar" >/dev/null 2>&1 || \
  { echo "ivy: the retrieved file is not a valid jar"; exit 1; }
echo "ivy: Apache Ivy resolved and retrieved $(basename "$jar") ($(wc -c <"$jar") bytes)"
