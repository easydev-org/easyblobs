#!/bin/sh
# maven lifecycle: mvn deploy / dependency:get public+private / upgrade / DELETE
# (maven has no client delete; the adapter exposes DELETE on the artifact path.)
# The artifact mount is the mirror for all repositories.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
G=io.easy-platform.lc
A="lc-probe-${SUFFIX}"

mkdir -p "$HOME/.m2"
cat > "$HOME/.m2/settings.xml" <<X
<settings>
  <mirrors>
    <mirror>
      <id>easy-platform</id>
      <url>$EASYBLOBS_BASE/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
X

deploy_artifact() { # $1 = version
  rm -rf "$WORK/proj"
  mkdir -p "$WORK/proj/src/main/java/io/easy-platform/lc"
  cat > "$WORK/proj/pom.xml" <<EOF
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>${G}</groupId>
  <artifactId>${A}</artifactId>
  <version>$1</version>
  <packaging>jar</packaging>
  <properties>
    <maven.compiler.release>21</maven.compiler.release>
    <project.build.sourceEncoding>UTF-8</project.build.sourceEncoding>
  </properties>
  <distributionManagement>
    <repository>
      <id>easy-platform</id>
      <url>${EASYBLOBS_BASE}</url>
    </repository>
  </distributionManagement>
</project>
EOF
  cat > "$WORK/proj/src/main/java/io/easy-platform/lc/LcProbe.java" <<EOF
package io.easy-platform.lc;
public final class LcProbe {
  public static final String VERSION = "$1";
  public static String version() { return VERSION; }
}
EOF
  mvn -q -B -Dmaven.repo.local="$HOME/m2" -f "$WORK/proj/pom.xml" deploy -DskipTests
}

repo_path="$HOME/m2/$(echo "$G" | tr . /)/$A"

case "$STAGE" in
publish)
  deploy_artifact "$V1"
  echo "maven: deployed ${G}:${A}:$V1"
  ;;
public)
  mvn -q -B -Dmaven.repo.local="$HOME/m2" dependency:get -Dartifact=junit:junit:4.13.2
  ls "$HOME"/m2/junit/junit/4.13.2/junit-4.13.2.jar >/dev/null
  echo "maven: public junit 4.13.2 ok"
  ;;
private)
  mvn -q -B -Dmaven.repo.local="$HOME/m2" dependency:get -Dartifact="${G}:${A}:${V1}"
  ls "$repo_path/$V1/$A-$V1.jar" >/dev/null
  echo "maven: private ${A}:$V1 ok"
  ;;
upgrade)
  deploy_artifact "$V2"
  mvn -q -B -Dmaven.repo.local="$HOME/m2" dependency:get -Dartifact="${G}:${A}:${V2}"
  ls "$repo_path/$V2/$A-$V2.jar" >/dev/null
  # maven-metadata.xml lists both versions.
  meta="$EASYBLOBS_BASE/$(echo "$G" | tr . /)/$A/maven-metadata.xml"
  wget -qO- "$meta" | grep -q "<version>$V1</version>"
  wget -qO- "$meta" | grep -q "<version>$V2</version>"
  echo "maven: upgraded to $V2, metadata lists both"
  ;;
delete)
  base="$EASYBLOBS_BASE/$(echo "$G" | tr . /)/$A"
  for v in "$V1" "$V2"; do
    code="$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$base/$v/$A-$v.jar")"
    [ "$code" = "200" ] || { echo "maven: delete $v jar rc=$code"; exit 1; }
    curl -s -o /dev/null -X DELETE "$base/$v/$A-$v.pom"
  done
  if mvn -q -B -Dmaven.repo.local="$HOME/m2" dependency:get -Dartifact="${G}:${A}:${V1}" >/dev/null 2>&1; then
    echo "maven: $V1 still resolvable after delete"; exit 1
  fi
  echo "maven: deleted ${A} {$V1,$V2}"
  ;;
esac
