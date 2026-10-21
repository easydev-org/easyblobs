#!/bin/sh
# CPAN mirrored at the named target: install a real distribution with cpanm.
#
# cpanm (App::cpanminus) is the client: it downloads the index AND the tarball,
# unpacks it, runs the build, and only then reports success - which is a much
# stronger statement than "the tarball downloaded".
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

# Point CPAN/cpanm at the mirror only.
mkdir -p /root/.cpan
cat > /root/.cpan/CPAN/MyConfig.pm <<PM
\$CPAN::Config = {
  'urllist' => ['$EASYBLOBS_BASE'],
  'build_dir' => q[/tmp/cpanbuild],
  'cpan_home' => q[/tmp/cpanhome],
  'mbuild_arg' => q[],
};
1;
PM
mkdir -p /tmp/cpanhome/CPAN
cat > /tmp/cpanhome/CPAN/MyConfig.pm <<PM
\$CPAN::Config = {
  'urllist' => ['$EASYBLOBS_BASE'],
  'build_dir' => q[/tmp/cpanbuild],
  'cpan_home' => q[/tmp/cpanhome],
};
1;
PM

export PERL_MM_USE_DEFAULT=1
export PERL_CPANM_OPT="--mirror $EASYBLOBS_BASE --mirror-only"

if ! command -v cpanm >/dev/null; then
  echo "cpan: cpanm not installed"; exit 1
fi
cpanm --notest --quiet DBI
perl -MDBI -e 'print "cpan: DBI ", $DBI::VERSION, " loaded\n"'
