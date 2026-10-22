fix(e2e): 25/40 green on the action architecture; the rest were root-only paths

The new architecture works: 25 of 40 protocols passed on the first run,
including every one whose client came from an action.

The 17 failures were almost all one thing: the suites assume they run as root.

  mkdir: cannot create directory '/w': Permission denied
  mkdir: cannot create directory '/root': Permission denied
  mkdir: cannot create directory '/rocktree': Permission denied

Fixed by making the paths configurable instead of absolute:

  /w          -> ${E2E_W:-/w}   (exported by ci-run.sh; 12 suites touched)
  /root/...   -> $HOME/...      (cpan, luarocks, maven, nuget)
  /rocktree   -> $HOME/rocktree (luarocks)

mvn family: `mirrorOf: *` redirected Maven's OWN dependency plugin to a
single-vendor mirror (dl.google.com, repo.clojars.org, jitpack.io), which does
not carry it:

  [ERROR] org.apache.maven.plugins:maven-dependency-plugin:jar:3.6.1
          was not found in http://.../maven/_upstream/jitpack.io

The mirror is now declared as a normal <repository> (e2e/maven-settings.sh), so
the tooling still comes from Central while the artifact under test comes from
the mount. -U also defeats Maven's cached "not found".

opam: the action already created a root and a switch; a second opam init against
a different root left opam with no switch, so opam list failed. The suite now
reuses the action's root and verifies a package through opam show.
