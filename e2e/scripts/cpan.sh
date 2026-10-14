#!/bin/sh
# CPAN (Perl): a plain HTTP tree. Fetch the package index and a distribution
# from the artifact mount (which mirrors cpan.metacpan.org) — what cpanm does
# before it builds a module.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget --header='Range: bytes=0-131071' -qO /tmp/02packages "$EASYBLOBS_BASE/modules/02packages.details.txt.gz"
[ -s /tmp/02packages ] || { echo "cpan: empty index"; exit 1; }
wget -qO /tmp/DBI.tar.gz "$EASYBLOBS_BASE/authors/id/T/TI/TIMB/DBI-1.643.tar.gz"
tar -tzf /tmp/DBI.tar.gz >/dev/null || { echo "cpan: bad tarball"; exit 1; }
echo "cpan: 02packages + DBI-1.643.tar.gz ok"
