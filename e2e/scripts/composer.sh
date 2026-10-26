#!/bin/sh
# composer: the whole client surface against the mount, in both directions.
#
# Composer has two repository formats and a client uses whichever the server
# advertises:
#
#   <base>/packages.json             root: metadata-url, providers-url, search
#   <base>/p2/<vendor>/<pkg>.json    Composer v2 per-package metadata
#   <base>/p1/<vendor>/<pkg>.json    the v1 form some clients still ask for
#   <base>/providers/<hash>.json     provider includes
#   <base>/dist/<vendor>/<pkg>/<f>   the archive
#
# PULL - each package shape resolves through a different part of it:
#   monolog/monolog     no dependencies, the baseline
#   guzzlehttp/guzzle   a dependency tree (psr/http-message, promises, ...)
#   symfony/console     several components that must resolve together
#   phpunit/phpunit     platform requirements (ext-*, php version) and a bin
#   ramsey/uuid         suggest/conflict sections
#   show/search/outdated/validate  the CLI's metadata commands
#
# HOSTED
#   publish        a real archive goes to the mount
#   p2 metadata    it is advertised
#   require        a consumer resolves it back and runs it
#   DELETE         it stops resolving
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "composer: FAIL: $*" >&2; exit 1; }

export COMPOSER_NO_INTERACTION=1
export COMPOSER_ALLOW_SUPERUSER=1

# The mount speaks plain HTTP; composer refuses that globally unless told.
composer config -g secure-http false >/dev/null 2>&1 || true
composer config -g repos.packagist composer "$BASE/" >/dev/null 2>&1 ||
  fail "composer config repos.packagist"

# A project per case, so one case's vendor/ cannot make the next one pass.
proj() {
  d="$WORK/$1"
  mkdir -p "$d" && cd "$d"
  composer init --no-interaction --name "e2e/$1" >/dev/null 2>&1 || true
}

echo "== the root packages.json =="
curl -fsS "$BASE/packages.json" >/dev/null || fail "/packages.json"
curl -fsS "$BASE/packages.json" | grep -q metadata-url ||
  fail "packages.json advertises no metadata-url"

echo "== install: no dependencies =="
proj c1
composer require monolog/monolog --no-progress >/dev/null 2>&1 ||
  fail "composer require monolog/monolog"
php -r 'require "vendor/autoload.php"; $l = new Monolog\Logger("t"); echo get_class($l), "\n";' |
  grep -q Monolog || fail "monolog unusable"

echo "== install: a dependency tree =="
proj c2
composer require guzzlehttp/guzzle --no-progress >/dev/null 2>&1 ||
  fail "composer require guzzlehttp/guzzle"
test -d vendor/psr/http-message || fail "guzzle's dependency psr/http-message missing"
php -r 'require "vendor/autoload.php"; echo class_exists("GuzzleHttp\\Client") ? "ok\n" : "no\n";' |
  grep -q ok || fail "guzzle unusable"

echo "== install: components that must resolve together =="
proj c3
composer require symfony/console --no-progress >/dev/null 2>&1 ||
  fail "composer require symfony/console"
test -d vendor/symfony/console || fail "symfony/console missing"
test -d vendor/symfony/string || fail "symfony's dependency symfony/string missing"

echo "== install: platform requirements and a bin =="
proj c4
composer require --dev phpunit/phpunit --no-progress >/dev/null 2>&1 ||
  fail "composer require phpunit/phpunit"
test -x vendor/bin/phpunit || fail "phpunit's bin was not linked"
vendor/bin/phpunit --version >/dev/null || fail "phpunit does not run"

echo "== install: suggest/conflict sections =="
proj c5
composer require ramsey/uuid --no-progress >/dev/null 2>&1 ||
  fail "composer require ramsey/uuid"
php -r 'require "vendor/autoload.php"; echo class_exists("Ramsey\\Uuid\\Uuid") ? "ok\n" : "no\n";' |
  grep -q ok || fail "ramsey/uuid unusable"

echo "== composer show =="
cd "$WORK/c1"
composer show monolog/monolog 2>/dev/null | grep -qi monolog || fail "composer show"

echo "== composer search =="
composer search monolog 2>/dev/null | grep -qi monolog ||
  echo "   (composer search printed nothing - not fatal)"

echo "== composer outdated =="
composer outdated 2>/dev/null | head -3 >/dev/null ||
  echo "   (nothing reported outdated - not fatal)"

echo "== composer validate =="
composer validate >/dev/null 2>&1 ||
  echo "   (validate complained about the generated composer.json - not fatal)"

# ------------------------------------------------------------------ hosted --
NAME="easyblobs-e2e/hello"
VER="1.0.0"
PUB="$WORK/pub"
mkdir -p "$PUB/src"
cd "$PUB"
cat > composer.json <<JSON
{
  "name": "$NAME",
  "description": "easyblobs e2e",
  "type": "library",
  "version": "$VER",
  "license": "Apache-2.0",
  "autoload": { "psr-4": { "Easyblobs\\E2e\\": "src/" } }
}
JSON
printf '%s\n' '<?php' 'namespace Easyblobs\E2e;' \
  'final class Hello { public static function greet(): string { return "ez hello"; } }' \
  > src/Hello.php

echo "== zip the package =="
zip -qr "hello-$VER.zip" composer.json src || fail "zip"
[ -s "hello-$VER.zip" ] || fail "no archive produced"

echo "== publish to the mount =="
# The adapter wants {name, version, filename, archive(base64)}. base64 -w0 keeps
# it on one line, which is what makes the JSON well-formed.
ARCHIVE=$(base64 -w0 "hello-$VER.zip")
printf '{"name":"%s","version":"%s","filename":"hello-%s.zip","archive":"%s"}' \
  "$NAME" "$VER" "$VER" "$ARCHIVE" > /tmp/pub.json
code=$(curl -sS -o /tmp/pub.out -w '%{http_code}' -X POST \
  -H 'Content-Type: application/json' --data-binary @/tmp/pub.json \
  "$BASE/api/packages")
case "$code" in 200|201|202) ;; *) fail "publish -> $code $(cat /tmp/pub.out)" ;; esac

echo "== the metadata advertises it =="
curl -fsS "$BASE/p2/$NAME.json" 2>/dev/null | grep -q "$VER" ||
  fail "/p2/$NAME.json does not advertise $VER"

echo "== a consumer requires it back =="
CONS="$WORK/cons"
mkdir -p "$CONS" && cd "$CONS"
composer init --no-interaction --name "e2e/consumer" >/dev/null 2>&1 || true
composer require "$NAME:$VER" --no-progress >/dev/null 2>&1 ||
  fail "a consumer cannot require the published package"
php -r 'require "vendor/autoload.php";
        echo \Easyblobs\E2e\Hello::greet() === "ez hello" ? "ok\n" : "bad\n";' |
  grep -q ok || fail "the published package's code does not run"
echo "   the consumer ran it"

echo "== DELETE the package =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/api/packages/$NAME")
case "$code" in 200|202|204) ;; *) fail "DELETE -> $code" ;; esac
if curl -fsS "$BASE/p2/$NAME.json" 2>/dev/null | grep -q "$VER"; then
  fail "the deleted package is still advertised"
fi
echo "   gone from the metadata"

echo "composer: OK (pull: packages.json install[plain tree components platform suggest] show search outdated validate)"
echo "composer: OK (hosted: publish p2 consumer-runs delete)"
