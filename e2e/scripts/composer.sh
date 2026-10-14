#!/bin/sh
# Composer: point the repository at the artifact mount (Packagist-compatible).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
cd /tmp
composer config -g secure-http false
composer config -g repos.packagist composer "$EASYBLOBS_BASE/"
mkdir -p /w && cd /w
composer require monolog/monolog --no-interaction --no-progress
# Run assertion: load the installed library via the generated autoloader.
php -r 'require "/w/vendor/autoload.php"; $l=new Monolog\Logger("t"); echo "php + monolog: ", get_class($l), "\n";'
