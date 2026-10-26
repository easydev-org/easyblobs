#!/bin/sh
# cargo: the whole client surface against the mount, in both directions.
#
# Cargo's sparse registry protocol is:
#
#   <base>/index/config.json      how the index describes itself (dl, api)
#   <base>/<ab>/<cd>/<crate>      the sparse index entry, NOT under index/ -
#                                 the client appends the path to the base
#   api/v1/crates/new             publish (a binary blob, not JSON)
#   api/v1/crates/<n>/<v>/download   the .crate
#   api/v1/crates/<n>/<v>/yank    and /unyank
#   api/v1/crates?q=              search
#
# PULL - each shape takes a different path through the resolver:
#   anyhow        no dependencies, the baseline
#   serde         a feature-gated derive, so the feature must resolve
#   reqwest       a large dependency tree (tokio, hyper, ...)
#   libc          a build script (build.rs) must run
#   cargo build/run/test  compile, link and execute
#
# HOSTED
#   cargo publish   a real crate goes to the mount
#   the index       the sparse entry lists the version
#   cargo add/build a consumer resolves it back and runs it
#   cargo yank      the version is marked, not deleted
#   cargo unyank    the mark is removed again
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "cargo: FAIL: $*" >&2; exit 1; }

export CARGO_HOME="$WORK/cargo-home"
mkdir -p "$CARGO_HOME"
cat > "$CARGO_HOME/config.toml" <<CONF
[source.crates-io]
replace-with = "mount"

[source.mount]
registry = "sparse+$BASE/"

[registries.mount]
index = "sparse+$BASE/"

CONF

# The token has to be filed per registry, either with `cargo login --registry
# mount` or as CARGO_REGISTRIES_MOUNT_TOKEN. A bare `[registry] token = ...`
# in config.toml is not read:
#   error: no token found for `mount`, please run `cargo login --registry mount`
export CARGO_REGISTRIES_MOUNT_TOKEN=e2e

echo "== the index describes itself =="
curl -fsS "$BASE/config.json" | grep -q dl || fail "index/config.json has no dl"
curl -fsS "$BASE/config.json" | grep -q api || fail "index/config.json has no api"

echo "== a sparse index entry is served at the hashed path =="
# anyhow -> an/yh/anyhow per cargo's path scheme.
curl -fsS "$BASE/an/yh/anyhow" >/dev/null || fail "the sparse entry for anyhow"

echo "== search =="
curl -fsS "$BASE/api/v1/crates?q=anyhow" >/dev/null || fail "api/v1/crates?q="

echo "== me (the identity endpoint) =="
curl -fsS "$BASE/me" | grep -qi anonymous || fail "/me"

echo "== build: no dependencies =="
mkdir -p p1/src && cd p1
printf '%s\n' '[package]' 'name = "p1"' 'version = "0.1.0"' 'edition = "2021"' '' \
  '[dependencies]' 'anyhow = "1"' > Cargo.toml
printf '%s\n' 'fn main() {' \
  '    let e = anyhow::anyhow!("boom");' \
  '    println!("cargo + anyhow: {}", e);' \
  '}' > src/main.rs
cargo run --quiet 2>/dev/null | grep -q 'cargo + anyhow' || fail "anyhow is not usable"
cd "$WORK"

echo "== build: a feature-gated derive =="
mkdir -p p2/src && cd p2
printf '%s\n' '[package]' 'name = "p2"' 'version = "0.1.0"' 'edition = "2021"' '' \
  '[dependencies]' 'serde = { version = "1", features = ["derive"] }' > Cargo.toml
printf '%s\n' 'use serde::{Serialize, Deserialize};' \
  '#[derive(Serialize, Deserialize, Debug)]' \
  'struct S { a: i32 }' \
  'fn main() { println!("serde: {:?}", S { a: 1 }); }' > src/main.rs
cargo run --quiet 2>/dev/null | grep -q 'serde: S' || fail "serde derive is not usable"
cd "$WORK"

echo "== build: a large dependency tree =="
mkdir -p p3/src && cd p3
printf '%s\n' '[package]' 'name = "p3"' 'version = "0.1.0"' 'edition = "2021"' '' \
  '[dependencies]' 'reqwest = { version = "0.11", default-features = false }' > Cargo.toml
printf '%s\n' 'fn main() { println!("reqwest: {}", std::any::type_name::<reqwest::Client>()); }' > src/main.rs
# Only fetch: compiling the whole tree is slow and adds nothing here.
cargo fetch --quiet 2>/dev/null || fail "cargo fetch for reqwest's tree"
ls "$CARGO_HOME/registry" | grep -q . || fail "nothing landed in the cargo registry cache"
cargo build --quiet 2>/dev/null || fail "reqwest did not build"
cd "$WORK"

echo "== build: a crate with a build script =="
mkdir -p p4/src && cd p4
printf '%s\n' '[package]' 'name = "p4"' 'version = "0.1.0"' 'edition = "2021"' '' \
  '[dependencies]' 'libc = "0.2"' > Cargo.toml
# getpid returns pid_t, which is not comparable to the integer literal without a
# cast - the first version of this line failed to compile and read like a broken
# package.
printf '%s\n' \
  'fn main() {' \
  '    // getpid is an unsafe fn: calling it outside an unsafe block is E0133,' \
  '    // which is what the earlier version of this test reported.' \
  '    let pid = unsafe { libc::getpid() };' \
  '    println!("libc: {}", pid > 0);' \
  '}' > src/main.rs
#
# stderr is kept, and the failure prints it: 'cargo run --quiet 2>/dev/null' hid
# whether this was a compile error or a missing package, which is exactly the
# distinction that matters.
if ! out=$(cargo run --quiet 2>&1); then
  echo "cargo: libc crate failed to build:" >&2
  echo "$out" | tail -25 >&2
  fail "libc is not usable"
fi
echo "$out" | grep -q 'libc: true' || fail "libc ran but printed: $out"
cd "$WORK"

# ------------------------------------------------------------------ hosted --
CRATE="easyblobs-e2e-crate"
VER="1.0.0"
mkdir -p "$WORK/crate/src"
cd "$WORK/crate"
printf '%s\n' \
  '[package]' \
  "name = \"$CRATE\"" \
  "version = \"$VER\"" \
  'edition = "2021"' \
  'description = "easyblobs e2e"' \
  'license = "Apache-2.0"' \
  '' \
  '[lib]' \
  'path = "src/lib.rs"' > Cargo.toml
printf '%s\n' \
  'pub fn greet() -> &'"'"'static str { "ez hello" }' \
  '' \
  '#[cfg(test)]' \
  'mod tests {' \
  '    #[test]' \
  '    fn it_greets() { assert_eq!(super::greet(), "ez hello"); }' \
  '}' > src/lib.rs

echo "== cargo test on the crate itself =="
cargo test --quiet 2>/dev/null | grep -qi 'test result' || fail "the crate's own test does not pass"

echo "== cargo package + publish =="
cargo package --quiet --no-verify 2>/dev/null || fail "cargo package"
# cargo publish refuses a dirty tree and wants the registry to answer; the mount
# implements the real publish endpoint, so this exercises it end to end.
cargo publish --quiet --registry mount --no-verify --allow-dirty 2>&1 | tail -3 ||
  fail "cargo publish"

echo "== the sparse index lists the version =="
# Cargo's sparse path scheme, exactly:
#   1 char   -> 1/<name>
#   2 chars  -> 2/<name>
#   3 chars  -> 3/<first>/<name>
#   otherwise-> <first2>/<next2>/<name>
#
# My first version built "<first2>/<last4>", which produced ea/rate for
# easyblobs-e2e-crate and then reported the index as broken.
LEN=$(printf '%s' "$CRATE" | wc -c)
case "$LEN" in
  1) P="1/$CRATE" ;;
  2) P="2/$CRATE" ;;
  3) P="3/$(printf '%s' "$CRATE" | cut -c1)/$CRATE" ;;
  *) P="$(printf '%s' "$CRATE" | cut -c1-2)/$(printf '%s' "$CRATE" | cut -c3-4)/$CRATE" ;;
esac
# Ask the index for the crate the way cargo would, then grep the version.
ENTRY=$(curl -fsS "$BASE/$P" 2>/dev/null || curl -fsS "$BASE/index/$P" 2>/dev/null || true)
[ -n "$ENTRY" ] || fail "the sparse entry for $CRATE is not served (tried $P)"
echo "$ENTRY" | grep -q "$VER" || fail "the sparse entry does not list $VER"

echo "== a consumer resolves it back =="
CONS="$WORK/consumer"
mkdir -p "$CONS/src"
cd "$CONS"
printf '%s\n' '[package]' 'name = "consumer"' 'version = "0.1.0"' 'edition = "2021"' '' \
  '[dependencies]' "$CRATE = \"$VER\"" > Cargo.toml
printf '%s\n' "fn main() { println!(\"{}\", $CRATE::greet()); }" > src/main.rs
cargo run --quiet 2>/dev/null | grep -q 'ez hello' ||
  fail "the published crate cannot be used by a consumer"
echo "   the consumer ran it"

echo "== cargo yank / unyank =="
cd "$WORK/crate"
cargo yank --registry mount --version "$VER" 2>&1 | tail -2 || fail "cargo yank"
cargo yank --registry mount --version "$VER" --undo 2>&1 | tail -2 || fail "cargo unyank"
echo "   yanked and unyanked"

echo "cargo: OK (pull: config sparse-entry me search build[plain features large-tree build-script])"
echo "cargo: OK (hosted: test package publish index consumer-resolves yank unyank)"
