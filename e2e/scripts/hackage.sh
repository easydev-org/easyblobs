#!/bin/sh
# Hackage (Haskell): a plain HTTP tree. Fetch the package index and a package
# tarball from the artifact mount (which mirrors hackage.haskell.org).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# The 01-index tarball is large (100MB+); fetch only its head via Range so the
# test stays quick while still exercising the pull-through path.
wget --header='Range: bytes=0-65535' -qO /tmp/idx "$EASYBLOBS_BASE/01-index.tar.gz"
[ -s /tmp/idx ] || { echo "hackage: empty index"; exit 1; }
wget -qO /tmp/pkg.tar.gz "$EASYBLOBS_BASE/package/aeson-2.2.3.0/aeson-2.2.3.0.tar.gz"
tar -tzf /tmp/pkg.tar.gz >/dev/null || { echo "hackage: bad tarball"; exit 1; }
echo "hackage: index + aeson-2.2.3.0.tar.gz ok"
