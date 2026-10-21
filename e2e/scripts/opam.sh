#!/bin/sh
# opam repository mirrored at the named target: use the REAL opam client.
#
# opam initialises a root, fetches the index tarball, and resolves a package
# (dune) from it - the index handling a wget probe never touches.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
command -v opam >/dev/null || { echo "opam: the opam binary is missing"; exit 1; }

export OPAMROOT=/tmp/opamroot
rm -rf "$OPAMROOT"
mkdir -p "$OPAMROOT"

opam init --bare --yes --disable-sandboxing \
  "easyblobs" "$EASYBLOBS_BASE" >/tmp/opam-init.log 2>&1 || {
    echo "opam: init against the mirror failed"; tail -10 /tmp/opam-init.log; exit 1; }

opam list --all --short 2>/dev/null | grep -qx dune || {
  echo "opam: dune is not in the mirrored index"; exit 1; }
echo "opam: initialised from the mirror and the index lists dune"
