#!/bin/sh
# Maven: add an artifact mirror for central in settings.xml, so all resolution
# goes through the artifact mount. The mount is a Maven-layout repository.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
mkdir -p /w && cd /w
cat > pom.xml <<'X'
<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion><groupId>t</groupId><artifactId>t</artifactId><version>1.0</version>
<dependencies><dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.9</version></dependency></dependencies>
</project>
X
cat > Main.java <<'X'
public class Main {
  public static void main(String[] a) {
    System.out.println("java + slf4j: " + org.slf4j.LoggerFactory.class.getName());
  }
}
X
mkdir -p /root/.m2
cat > /root/.m2/settings.xml <<X
<settings>
  <mirrors>
    <mirror>
      <id>artifact</id>
      <name>artifact maven mirror</name>
      <url>$EASYBLOBS_BASE/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
X
# Resolve the dependency through the mirror, then compile+run against it.
mvn -q -B -Dmaven.repo.local=/tmp/m2 org.apache.maven.plugins:maven-dependency-plugin:3.6.1:resolve
CP="/tmp/m2/org/slf4j/slf4j-api/2.0.9/slf4j-api-2.0.9.jar"
javac -cp "$CP" Main.java
java -cp ".:$CP" Main
