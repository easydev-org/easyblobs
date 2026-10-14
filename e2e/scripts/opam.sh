#!/bin/sh
# opam repository (opam.ocaml.org): index tarball + a package's opam file,
# served from the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget --header='Range: bytes=0-65535' -qO /tmp/opam-idx "$EASYBLOBS_BASE/index.tar.gz"
[ -s /tmp/opam-idx ] || { echo "opam: empty index"; exit 1; }
wget -qO /tmp/opam-pkg "$EASYBLOBS_BASE/packages/dune/dune.3.17.2/opam"
grep -q 'opam-version' /tmp/opam-pkg || { echo "opam: bad opam file"; exit 1; }
echo "opam: index + dune opam ok"
