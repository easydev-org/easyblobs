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
  # Two ways in, in order of preference:
  #   1. CPAN::Shell, which uses the MyConfig.pm written above (the mirror).
  #   2. fetch the cpanm script from the mirror directly and run it.
  # Version 1 needs a working CPAN config; on a bare nix perl it is silent when
  # it fails, which is why the log is kept and printed.
  if ! perl -MCPAN -e 'CPAN::Shell->install("App::cpanminus")' >/tmp/cpanm-install.log 2>&1; then
    echo "cpan: CPAN::Shell install failed, falling back to the script:" >&2
    tail -12 /tmp/cpanm-install.log >&2
  fi
  # Any of these locations is where a CPAN install would have put it.
  #
  # An `[ -x ] && PATH=... && break` chain looks tidy and is wrong under `set -e`:
  # the loop's last command is the failed test, so the whole list aborts the
  # script instead of moving to the next candidate. A plain `if` does not.
  for d in "$HOME/.local/bin" /tmp/cpanhome/bin /usr/local/bin "$HOME/perl5/bin"; do
    if [ -x "$d/cpanm" ]; then
      PATH="$d:$PATH"
      export PATH
      break
    fi
  done
  if ! command -v cpanm >/dev/null; then
    # Last resort: cpanm is a single Perl script; the mirror serves it.
    echo "cpan: fetching the cpanm script from the mount" >&2
    mkdir -p "$HOME/.local/bin"
    if curl -fsS -o "$HOME/.local/bin/cpanm" "$EASYBLOBS_BASE/authors/id/M/MI/MIYAGAWA/App-cpanminus-1.7047.tar.gz" 2>/dev/null; then
      # That is the tarball, not the script; unpack and use its bin/cpanm.
      ( cd /tmp && tar -xzf "$HOME/.local/bin/cpanm" 2>/dev/null ) || true
      found=$(find /tmp/App-cpanminus-* -name cpanm -type f 2>/dev/null | head -1)
      [ -n "$found" ] && cp "$found" "$HOME/.local/bin/cpanm" && chmod +x "$HOME/.local/bin/cpanm"
    fi
    if [ -x "$HOME/.local/bin/cpanm" ]; then
      PATH="$HOME/.local/bin:$PATH"; export PATH
    fi
  fi
fi
command -v cpanm >/dev/null || { echo "cpan: cpanm still missing after install" >&2; exit 1; }

# The nix perl's site_perl is inside the read-only store, so cpanm falls back to
# ~/perl5:
#   Can't write to /nix/store/...-perl-5.42.3/lib/perl5/site_perl/5.42.3 ...
#   Installing modules to /home/runner/perl5
#   Successfully installed DBI-1.656
# and then the module is invisible because @INC does not include that path.
# local::lib is the supported way to make it visible:
#   eval $(perl -I ~/perl5/lib/perl5/ -Mlocal::lib)
# which is what cpanm itself suggests, so this is the normal local-lib layout and
# not a workaround for the mirror.
cpanm --notest --quiet DBI || { echo "cpan: cpanm could not install DBI" >&2; exit 1; }

PERLLIB="$HOME/perl5/lib/perl5"
if [ -d "$PERLLIB" ]; then
  PERL5LIB="$PERLLIB${PERL5LIB:+:$PERL5LIB}"
  export PERL5LIB
fi
perl -MDBI -e 'print "cpan: DBI ", $DBI::VERSION, " loaded\n"' ||
  { echo "cpan: DBI installed but not loadable (PERL5LIB=$PERL5LIB)" >&2; exit 1; }
