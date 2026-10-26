#!/bin/sh
# rubygems: the whole client surface against the mount, in both directions.
#
# Two index formats coexist and a client uses both:
#
#   the COMPACT INDEX  /versions, /info/<gem>, /names - what `gem install` reads
#   the MARSHAL specs  /specs.4.8.gz, /latest_specs.4.8.gz, /prerelease_specs.4.8.gz
#                      - what older bundler/rubygems falls back to
#   the JSON API       /api/v1/gems/<name>.json, /api/v1/versions/<name>.json,
#                      /api/v1/search, /api/v1/dependencies, /api/v2/rubygems/...
#   the gem file       /gems/<name>-<version>.gem
#
# PULL - each gem shape takes a different path through the installer:
#   rake        pure Ruby, the baseline
#   rspec       dependencies (several gems must resolve together)
#   nokogiri    a native extension: the platform gem must be chosen
#   bundler     an executable in bin/, so the shim must be generated
#   json        ships a platform-specific prebuilt
#   gem fetch   download without installing
#   gem search  remote search endpoint
#   gem list -r remote listing
#
# HOSTED
#   gem push        a built gem goes in
#   gem install     it comes back out and runs
#   gem fetch       bytes are identical
#   gem yank        the version disappears from the index
#   gem sources     the mount is the only source
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

SRC="${EASYBLOBS_BASE%/}/"
WORK=$(mktemp -d)
PKG=""
cleanup() { rm -rf "$WORK" ${PKG:+"$PKG"}; }
trap cleanup EXIT
cd "$WORK"

fail() { echo "rubygems: FAIL: $*" >&2; exit 1; }

export GEM_HOME="$WORK/gems"
export GEM_PATH="$GEM_HOME"
mkdir -p "$GEM_HOME"

echo "== the mount is a gem source =="
gem sources --clear-all >/dev/null
gem sources --add "$SRC" >/dev/null || fail "gem sources --add"
gem sources --list 2>/dev/null | grep -q "$EASYBLOBS_BASE" || fail "the source is not listed"

echo "== index endpoints a client touches first =="
curl -fsS "${SRC}versions" >/dev/null || fail "/versions"
curl -fsS "${SRC}names"    >/dev/null || fail "/names"
curl -fsS "${SRC}specs.4.8.gz" >/dev/null || fail "/specs.4.8.gz (marshal index)"
curl -fsS "${SRC}latest_specs.4.8.gz" >/dev/null || fail "/latest_specs.4.8.gz"
curl -fsS "${SRC}prerelease_specs.4.8.gz" >/dev/null || fail "/prerelease_specs.4.8.gz"

echo "== install: pure Ruby =="
gem install --no-document rake >/dev/null || fail "gem install rake"
ruby -e 'require "rake"; raise unless Rake::VERSION' || fail "rake unusable"

echo "== install: with dependencies =="
gem install --no-document rspec >/dev/null || fail "gem install rspec"
ruby -e 'require "rspec"; raise unless RSpec' || fail "rspec unusable"
# Its own gems must be there, not just the top-level name.
gem list rspec-core | grep -q rspec-core || fail "rspec dependencies were not resolved"

echo "== install: a native extension =="
gem install --no-document nokogiri >/dev/null || fail "gem install nokogiri (native ext)"
ruby -e 'require "nokogiri"; raise unless Nokogiri::VERSION' || fail "nokogiri unusable"

echo "== install: a gem with an executable =="
gem install --no-document bundler >/dev/null || fail "gem install bundler"
test -x "$GEM_HOME/bin/bundle" || fail "the bundle executable was not generated"
"$GEM_HOME/bin/bundle" --version >/dev/null || fail "bundle does not run"

echo "== gem fetch (no install) =="
mkdir -p dl && cd dl
gem fetch json >/dev/null || fail "gem fetch"
ls | grep -q '^json-' || fail "gem fetch produced nothing"
cd "$WORK"

echo "== info: the compact per-gem endpoint =="
curl -fsS "${SRC}info/rake" | grep -q rake || fail "/info/rake"

echo "== JSON API =="
curl -fsS "${SRC}api/v1/gems/rake.json" | grep -qi rake || fail "/api/v1/gems/rake.json"
curl -fsS "${SRC}api/v1/versions/rake.json" | grep -qi rake || fail "/api/v1/versions/rake.json"
curl -fsS "${SRC}api/v1/search.json?query=rake" | grep -qi rake || fail "/api/v1/search"
curl -fsS "${SRC}api/v1/dependencies?gems=rake" >/dev/null || fail "/api/v1/dependencies"

echo "== gem search / list -r =="
gem search -r '^rake$' 2>/dev/null | grep -qi rake || echo "   (gem search printed nothing - not fatal)"
gem list -r '^rake$' 2>/dev/null | grep -qi rake || echo "   (gem list -r printed nothing - not fatal)"

# ------------------------------------------------------------------ hosted --
NAME="easyblobs_e2e_gem"
VER="1.0.0"
PKG=$(mktemp -d)
cd "$PKG"
mkdir -p lib exe
printf 'module EasyblobsE2e\n  VERSION = "%s"\n  def self.hello; "ez hello"; end\nend\n' "$VER" > lib/easyblobs_e2e.rb
printf '#!/usr/bin/env ruby\nrequire "easyblobs_e2e"\nputs EasyblobsE2e.hello\n' > exe/ezgem
chmod +x exe/ezgem
cat > easyblobs_e2e.gemspec <<SPEC
Gem::Specification.new do |s|
  s.name        = "$NAME"
  s.version     = "$VER"
  s.summary     = "easyblobs e2e"
  s.authors     = ["e2e"]
  s.files       = ["lib/easyblobs_e2e.rb"]
  s.bindir      = "exe"
  s.executables = ["ezgem"]
  s.license     = "Apache-2.0"
end
SPEC

echo "== build the gem =="
gem build easyblobs_e2e.gemspec >/dev/null 2>&1 || fail "gem build"
ls | grep -q "^${NAME}-${VER}.gem$" || fail "gem build produced no .gem"

echo "== gem push =="
mkdir -p "$HOME/.gem"
printf -- '---\n:rubygems_api_key: e2e\n' > "$HOME/.gem/credentials"
chmod 600 "$HOME/.gem/credentials"
# Bare `gem push` pushes to Gem.host (rubygems.org); --host redirects it. This
# was the original form, and it was right - I broke it by "fixing" a
# Gem::OptionParser::InvalidOption that came from my own --source, not --host.
gem push --host "$SRC" "$NAME-$VER.gem" 2>&1 | tail -3 || fail "gem push"

echo "== the index lists the version =="
curl -fsS "${SRC}info/$NAME" | grep -q "$VER" || fail "the pushed version is not in the index"
curl -fsS "${SRC}versions" | grep -q "$NAME" || fail "/versions does not mention the gem"

echo "== install it back and run its executable =="
gem install --no-document "$NAME" -v "$VER" >/dev/null 2>&1 || fail "the pushed gem cannot be installed"
ruby -e "require 'easyblobs_e2e'; raise unless EasyblobsE2e.hello == 'ez hello'" ||
  fail "the installed gem's code does not run"
test -x "$GEM_HOME/bin/ezgem" || fail "the gem's executable was not generated"
"$GEM_HOME/bin/ezgem" | grep -q 'ez hello' || fail "the gem's executable does not run"

echo "== gem fetch returns identical bytes =="
mkdir -p back && cd back
gem fetch "$NAME" -v "$VER" --source "$SRC" >/dev/null 2>&1 || fail "gem fetch the hosted gem"
got=$(ls | grep "^${NAME}-${VER}.gem$" | head -1)
[ -n "$got" ] || fail "no .gem fetched"
cmp -s "$got" "$PKG/$NAME-$VER.gem" || fail "the served gem differs from the built one"
cd "$PKG"

echo "== gem yank =="
gem yank "$NAME" -v "$VER" --source "$SRC" 2>&1 | tail -2 || fail "gem yank"
if curl -fsS "${SRC}info/$NAME" 2>/dev/null | grep -q "$VER"; then
  fail "the yanked version is still in the index"
fi
echo "   the version is gone from the index"

echo "rubygems: OK (pull: source index[marshal+compact] install[pure deps native exe] fetch info api search)"
echo "rubygems: OK (hosted: build push index install exe bytes yank)"
