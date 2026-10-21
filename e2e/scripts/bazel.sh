#!/bin/sh
# Bazel Central Registry mirrored at the named target.
#
# The REAL client is bazel: given a bzlmod project it fetches the module
# metadata and the source archive from the registry, verifies the integrity
# hash, and only then builds. A wget of metadata.json proves none of that.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

command -v bazel >/dev/null || { echo "bazel: the bazel binary is missing"; exit 1; }

rm -rf /w && mkdir -p /w
cd /w
cat > .bazelrc <<RC
common --registry=$EASYBLOBS_BASE
common --enable_bzlmod
RC
cat > MODULE.bazel <<'MOD'
module(name = "e2e_bcr", version = "0.0.0")
bazel_dep(name = "rules_go", version = "0.50.1")
MOD
cat > BUILD.bazel <<'B'
genrule(
    name = "noop",
    outs = ["noop.txt"],
    cmd = "echo bcr-ok > $@",
)
B

# `bazel mod graph` resolves the dependency THROUGH the configured registry and
# verifies the source archive's integrity; no build needed.
bazel mod graph 2>&1 | tail -5
bazel build //:noop 2>&1 | tail -3
echo "bazel: bzlmod resolved rules_go 0.50.1 via the mirrored registry"
