#!/bin/sh
# RubyGems: point the gem source at the artifact mount (compact index).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
gem sources --clear-all >/dev/null
gem sources --add "$EASYBLOBS_BASE/" >/dev/null
gem install --no-document rake
# Run assertion: execute the installed gem.
ruby -e 'require "rake"; puts "ruby + rake: #{Rake::VERSION}"'
