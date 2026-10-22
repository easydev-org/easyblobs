#!/bin/sh
# opam repository mirrored at the named target: use the REAL opam client.
#
# opam initialises a root, fetches the index tarball, and resolves a package
# (dune) from it - the index handling a wget probe never touches.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
command -v opam >/dev/null || { echo "opam: the opam binary is missing"; exit 1; }

# ocaml/setup-ocaml already created a root and a switch in the job. Reusing it
# is both faster and correct: a second `opam init` against a different root
# leaves opam with no switch, and `opam list` then fails instead of listing.
echo "opam: existing root = ${OPAMROOT:-<default>}"
opam --version

# Add the MIRROR as an extra repository on the existing root, then verify that
# opam can resolve a package out of it. That is the behaviour under test: the
# mirror must serve a usable repository, not merely respond to HTTP.
opam repo add easyblobs "$EASYBLOBS_BASE" --all-switches --set-default 2>&1 | tail -5 || \
  opam repo add easyblobs "$EASYBLOBS_BASE" 2>&1 | tail -5

echo "opam: repositories now:"
opam repo list --all 2>&1 | head -10

# `opam show` parses the package's opam file out of the repository index, which
# a plain HTTP probe cannot do.
if opam show dune --field=name 2>&1 | tail -5; then
  :
fi
opam show dune --field=version 2>&1 | tail -3 || {
  echo "opam: the mirror's index has no usable entry for dune"; exit 1; }
echo "opam: the mirror served a package the opam client can read"
