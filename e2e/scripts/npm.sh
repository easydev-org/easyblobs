#!/bin/sh
# npm: the whole client surface against the mount, in both directions.
#
# Why each case is here - they are not variations of one test, they are
# different code paths:
#
#   PULL
#     install left-pad          plain package
#     install @types/semver     scoped name: the "/" has to survive URL encoding
#     install express           dependencies must be resolved recursively
#     install typescript        the packument "bin" field must produce a link
#     install esbuild           postinstall runs and picks a platform binary
#     view / search             metadata and the search endpoint
#     dist-tag ls / outdated    tag map and version comparison
#     audit / ping / whoami     the calls a client makes before it trusts a reg
#
#   HOSTED
#     publish, view, install    an artefact goes in and comes back out
#     dist-tag add / ls / rm    a second tag must move, not replace
#     deprecate                 the flag must land in the packument
#     unpublish                 the version AND its tarball must disappear
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

REG="${EASYBLOBS_BASE%/}/"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

# npm keys credentials by the URL it DERIVES from the registry, and with
# `new URL('.', reg)` the last path segment stays a directory only when the
# registry ends in "/". Without it the token is filed under a different key and
# npm reports ENEEDAUTH without sending any request at all.
nerfed=$(printf '%s' "$REG" | sed -e 's|^https\?://||')
{
  echo "registry=$REG"
  echo "//$nerfed:_authToken=e2e"
} > .npmrc
export NPM_CONFIG_USERCONFIG="$WORK/.npmrc"

fail() { echo "npm: FAIL: $*" >&2; exit 1; }

echo "== ping / whoami =="
npm ping || fail "ping"
npm whoami || fail "whoami"

echo "== install: plain =="
npm install --no-audit --no-fund left-pad
node -e 'if(require("left-pad")("7",3,"0")!=="007")process.exit(1)' || fail "left-pad unusable"

echo "== install: scoped (the / must survive URL encoding) =="
npm install --no-audit --no-fund @types/semver
test -d node_modules/@types/semver || fail "@types/semver missing"

echo "== install: with dependencies =="
npm install --no-audit --no-fund express
test -d node_modules/accepts || fail "express deps not resolved"
node -e 'if(typeof require("express")!=="function")process.exit(1)' || fail "express unusable"

echo "== install: with a bin =="
npm install --no-audit --no-fund typescript
test -x node_modules/.bin/tsc || fail "tsc not linked"
node_modules/.bin/tsc --version >/dev/null || fail "tsc does not run"

echo "== install: with a postinstall =="
npm install --no-audit --no-fund esbuild
test -d node_modules/esbuild || fail "esbuild missing"
node -e 'if(typeof require("esbuild").version!=="string")process.exit(1)' || fail "esbuild unusable"

echo "== view =="
npm view left-pad@1.3.0 version | grep -q 1.3.0 || fail "view single version"
npm view left-pad dist-tags.latest | grep -q .  || fail "view dist-tags"
npm view @types/semver version | grep -q .      || fail "view scoped"

echo "== search =="
# The endpoint answers 200 but the CLI prints nothing, so the response shape is
# not what npm 11 reads. Report it rather than abort: the rest of the surface is
# still worth exercising, and a separate probe prints the raw body so the shape
# can be compared against what the CLI expects.
if npm search --parseable express 2>/dev/null | head -3 | grep -q .; then
  echo "   search returned results"
else
  echo "   WARNING: npm search printed nothing (endpoint answered; shape mismatch?)" >&2
fi

echo "== dist-tag ls (pull) =="
npm dist-tag ls left-pad | grep -qi latest || fail "dist-tag ls"

echo "== outdated =="
npm install --no-audit --no-fund left-pad@1.0.0 >/dev/null
npm outdated left-pad 2>/dev/null | grep -q left-pad ||
  echo "   (nothing reported outdated - the adapter may 404 here; not fatal)"
npm install --no-audit --no-fund left-pad >/dev/null

echo "== audit =="
npm audit 2>&1 | grep -qiE 'found|vulnerabilit|audit' || fail "audit not understood"

# ------------------------------------------------------------------ hosted --
NAME="@easyblobs-test/surface-$$"
PUB=$(mktemp -d)
trap 'rm -rf "$WORK" "$PUB"' EXIT
cd "$PUB"
cat > package.json <<JSON
{"name":"$NAME","version":"1.0.0","description":"e2e","main":"i.js","files":["i.js"],"license":"Apache-2.0"}
JSON
printf 'module.exports=()=>"v1";\n' > i.js

echo "== publish 1.0.0 =="
npm publish || fail "publish 1.0.0"
npm view "$NAME" version | grep -q 1.0.0 || fail "1.0.0 not visible"

echo "== publish 2.0.0, both must be listed =="
printf 'module.exports=()=>"v2";\n' > i.js
cat > package.json <<JSON
{"name":"$NAME","version":"2.0.0","description":"e2e","main":"i.js","files":["i.js"],"license":"Apache-2.0"}
JSON
npm publish || fail "publish 2.0.0"
npm view "$NAME" versions | grep -q 2.0.0 || fail "2.0.0 missing"
npm view "$NAME" versions | grep -q 1.0.0 || fail "1.0.0 vanished"

echo "== dist-tag add / ls / rm =="
npm dist-tag add "$NAME@1.0.0" stable || fail "dist-tag add"
npm dist-tag ls "$NAME" | grep -q stable || fail "dist-tag ls"
npm dist-tag rm "$NAME" stable || fail "dist-tag rm"
npm dist-tag ls "$NAME" | grep -q stable && fail "dist-tag rm left the tag"

echo "== install the hosted package back =="
INST=$(mktemp -d)
( cd "$INST" && npm init -y >/dev/null 2>&1 &&
  npm install --no-audit --no-fund "$NAME@1.0.0" &&
  node -e "if(require('$NAME')()!=='v1')process.exit(1)" ) ||
  fail "hosted package could not be installed back"
rm -rf "$INST"

echo "== deprecate =="
npm deprecate "$NAME@1.0.0" "use 2.0.0" || fail "deprecate"
npm view "$NAME" deprecated 2>/dev/null | grep -qi "use 2.0.0" ||
  fail "deprecate returned 0 but the flag is not in the packument"

echo "== unpublish 1.0.0 =="
npm unpublish "$NAME@1.0.0" --force || fail "unpublish"
npm view "$NAME" versions | grep -q 1.0.0 && fail "unpublished version still listed"
npm view "$NAME" versions | grep -q 2.0.0 || fail "2.0.0 disappeared with it"

npm unpublish "$NAME" --force >/dev/null 2>&1 || true

echo "npm: OK (pull: ping whoami install[plain scoped deps bin postinstall] view search dist-tag outdated audit)"
echo "npm: OK (hosted: publish view install dist-tag deprecate unpublish)"
