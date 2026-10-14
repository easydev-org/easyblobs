#!/bin/sh
# npm lifecycle: publish / install public / install private / upgrade / unpublish
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
REG="$EASYBLOBS_BASE/"
# npm refuses to publish without a locally-known token; the open registry
# ignores its value. The registry is the artifact mount.
printf '%s:_authToken=lc-token\n' "$EASYBLOBS_BASE" > "$HOME/.npmrc"
NPM="npm --registry=$REG"
case "$STAGE" in
publish|upgrade)
  # A: publish $VER (upgrade publishes 2.0.0 after 1.0.0 exists).
  mkdir -p "$WORK/pub" && cd "$WORK/pub"
  cat > package.json <<EOF
{ "name": "$NAME", "version": "$VER", "description": "lc probe",
  "main": "index.js" }
EOF
  printf 'module.exports = { version: "%s", hello: () => "lc-%s" };\n' "$VER" "$VER" > index.js
  $NPM publish --access public --no-audit --no-fund
  [ "$STAGE" = "publish" ] && exit 0
  # B (fresh): v2 installs and runs; both versions are listed.
  cd "$WORK" && $NPM install --no-audit --no-fund --no-package-lock --prefix "$WORK/use" "$NAME@$V2" >/dev/null
  cd "$WORK/use"
  node -e "const m=require('$NAME'); if(m.version!=='$V2' || m.hello()!=='lc-$V2') process.exit(1)"
  $NPM view "$NAME" versions --json | grep -q "\"$V1\""
  $NPM view "$NAME" versions --json | grep -q "\"$V2\""
  echo "npm: $NAME $V1+$V2"
  ;;
public)
  cd "$WORK"
  $NPM install --no-audit --no-fund --no-package-lock left-pad >/dev/null
  node -e 'const p=require("left-pad"); if(p("7",3,"0")!=="007") process.exit(1)'
  echo "npm: public left-pad ok"
  ;;
private)
  cd "$WORK"
  $NPM install --no-audit --no-fund --no-package-lock "$NAME@$V1" >/dev/null
  node -e "const m=require('$NAME'); if(m.version!=='$V1') process.exit(1)"
  echo "npm: private $NAME@$V1 ok"
  ;;
delete)
  $NPM unpublish "$NAME" --force >/dev/null
  if $NPM view "$NAME" version >/dev/null 2>&1; then
    echo "npm: $NAME still present after unpublish"; exit 1
  fi
  echo "npm: $NAME unpublished"
  ;;
esac
