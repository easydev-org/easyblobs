#!/bin/sh
# maven-settings.sh — emit a Maven settings.xml that adds the mirror as a
# REPOSITORY (not as a `mirrorOf: *`).
#
# Why this exists: `mirrorOf: *` redirects EVERYTHING to the mount, including
# maven-dependency-plugin itself. A single-repository mirror (dl.google.com,
# repo.clojars.org, jitpack.io, ...) does not carry Maven's own plugins, so the
# plugin cannot be resolved and the run fails before it tests anything:
#
#   [ERROR] org.apache.maven.plugins:maven-dependency-plugin:jar:3.6.1
#           was not found in http://.../maven/_upstream/jitpack.io
#
# Declaring the mount as a normal <repository> keeps Maven Central available for
# the tooling while the ARTIFACT under test still comes from the mount.
#
# Usage: maven-settings.sh <mount-url> <local-repo-dir> > settings.xml
set -eu

mount="${1:?usage: maven-settings.sh <mount-url> <local-repo-dir>}"
repo="${2:?usage: maven-settings.sh <mount-url> <local-repo-dir>}"

cat <<XML
<settings>
  <localRepository>$repo</localRepository>
  <profiles>
    <profile>
      <id>easyblobs</id>
      <repositories>
        <repository>
          <id>easyblobs</id>
          <name>easyblobs mirror</name>
          <url>$mount</url>
          <releases><enabled>true</enabled><updatePolicy>always</updatePolicy></releases>
          <snapshots><enabled>true</enabled><updatePolicy>always</updatePolicy></snapshots>
        </repository>
      </repositories>
    </profile>
  </profiles>
  <activeProfiles><activeProfile>easyblobs</activeProfile></activeProfiles>
</settings>
XML
