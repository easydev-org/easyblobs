#!/bin/sh
# pub.dev (Dart): point PUB_HOSTED_URL at the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export PUB_HOSTED_URL="$EASYBLOBS_BASE"
dart pub cache add http --version 1.2.2
# Compile/run assertion: build a small program against a cached package.
rm -rf /w && mkdir -p ${E2E_W:-/w}/bin
cat > ${E2E_W:-/w}/pubspec.yaml <<'X'
name: w
environment:
  sdk: ">=3.0.0 <4.0.0"
dependencies:
  path: any
X
cat > ${E2E_W:-/w}/bin/main.dart <<'X'
import 'package:path/path.dart' as p;
void main() => print('dart + path: ' + p.join('a', 'b'));
X
cd ${E2E_W:-/w}
dart pub get >/dev/null
dart run bin/main.dart
