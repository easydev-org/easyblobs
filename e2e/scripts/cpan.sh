#!/bin/sh
# CPAN mirrored at the named target: install a real distribution with cpanm.
#
# cpanm (App::cpanminus) is the client: it downloads the index AND the tarball,
# unpacks it, runs the build, and only then reports success - which is a much
# stronger statement than "the tarball downloaded".
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

# Point CPAN/cpanm at the mirror only.
mkdir -p $HOME/.cpan/CPAN
cat > $HOME/.cpan/CPAN/MyConfig.pm <<PM
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

# cpanm is not shipped by the perl toolchain any more (the third-party setup
# action used to bundle it), and it is not a setup of its own. Installing it
# THROUGH the mount is the honest path: it proves the mirror can serve a client
# its own build tool, which is what a real air-gapped user would do.
if ! command -v cpanm >/dev/null; then
  echo "cpan: cpanm is absent; installing it from the mount"
  perl -MCPAN -e 'CPAN::Shell->install("App::cpanminus")' >/tmp/cpanm-install.log 2>&1 || {
    echo "cpan: could not install App::cpanminus through the mirror" >&2
    tail -20 /tmp/cpanm-install.log >&2
    exit 1; }
  for d in "$HOME/.local/bin" /tmp/cpanhome/bin /usr/local/bin; do
    [ -x "$d/cpanm" ] && PATH="$d:$PATH" && export PATH && break
  done
fi
command -v cpanm >/dev/null || { echo "cpan: cpanm still missing after install" >&2; exit 1; }

cpanm --notest --quiet DBI
perl -MDBI -e 'print "cpan: DBI ", $DBI::VERSION, " loaded\n"'
