#!/bin/sh
# Julia: the package server is a plain HTTP tree. Point JULIA_PKG_SERVER at the
# artifact mount and let Pkg do a registry + package install.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export JULIA_PKG_SERVER="$EASYBLOBS_BASE"
export JULIA_DEPOT_PATH=/tmp/julia-depot
d=$(mktemp -d)
cd "$d"
julia -e 'using Pkg; Pkg.Registry.add("General"); Pkg.add("JSON2")' >/dev/null 2>&1
julia --project=. -e 'using JSON2; println("julia: JSON2 loaded")'
