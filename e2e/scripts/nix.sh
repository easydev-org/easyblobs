#!/bin/sh
# Nix: point the binary cache at the artifact mount. Nix's binary-cache URL is
# the mount base (it appends /nix-cache-info and /<hash>.narinfo).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export NIX_CONFIG="experimental-features = nix-command flakes"
nix --version
nix store info --store "$EASYBLOBS_BASE"
# Evaluation assertion: parse + evaluate a Nix expression and a tiny derivation.
nix-instantiate --eval -E '1 + 1'
nix-instantiate --eval -E 'builtins.length [ "a" "b" "c" ]'
