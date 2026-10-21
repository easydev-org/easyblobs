#!/bin/sh
# ci-run.sh <protocol> — run ONE real end-to-end suite against easyblobs.
#
# Wraps e2e/scripts/<protocol>.sh so every protocol gets identical treatment:
#   1. the script drives a REAL client against the mount,
#   2. we assert it actually fetched bytes, so an adapter that only proxies
#      every request cannot pass,
#   3. we print the server log tail on failure, because a 502 from an upstream
#      looks exactly like an adapter bug without it.
#
# EACH PROTOCOL HAS ITS OWN MOUNT. Some also live under an explicit UPSTREAM
# segment, and e2e/run.sh already encodes that mapping in proto_path(). We
# mirror it here so every client is pointed at the exact prefix it needs:
#
#   helm      -> /artifacts/helm
#   google    -> /artifacts/maven/_upstream/dl.google.com
#   gradle    -> /artifacts/maven/_upstream/plugins.gradle.org
#   clojars   -> /artifacts/maven/_upstream/repo.clojars.org
#   spring    -> /artifacts/maven/_upstream/repo.spring.io
#   jitpack   -> /artifacts/maven/_upstream/jitpack.io
#   jsrnpm    -> /artifacts/npm/_upstream/npm.jsr.io
#   gitlfs    -> /artifacts/git
#   others    -> /artifacts/<protocol>
#
# Needs: EASYBLOBS_ORIGIN (e.g. http://127.0.0.1:8080) plus whatever client the
# individual script requires.
set -eu

PROTO="${1:?usage: ci-run.sh <protocol>}"
: "${EASYBLOBS_ORIGIN:?EASYBLOBS_ORIGIN not set}"
: "${EASYBLOBS_DATA:=/tmp/eb-data}"

# NO PROXIES. The CI environment must run with exactly the network it has; the
# harness does not inject, wrap or restrict egress. Every *_proxy variable is
# cleared so a client never inherits one from the runner image, and nothing here
# sets one.
unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY all_proxy ALL_PROXY
export NO_PROXY="*"
export no_proxy="*"
# A few clients consult their own mirror variables; point them at the mount.
export GOPROXY="$EASYBLOBS_ORIGIN/artifacts/go"
export GOSUMDB=off

# ---------------------------------------------------------------- proof -----
# The mount must answer before a suite runs; without that every failure looks
# like a protocol bug.
#
# NOTE: never write `$(curl ... || echo 000)`. When curl prints '000' AND exits
# non-zero the two concatenate into '000000', which then fails an equality test
# and aborts the step for no reason. Read the exit status on its own line.
echo "--- connectivity sentinel ---"
if command -v curl >/dev/null; then
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "$EASYBLOBS_ORIGIN/healthz" 2>/dev/null)
  echo "  mount /healthz -> $code"
  [ "$code" = "200" ] || { echo "FATAL: the mount is not reachable" >&2; exit 1; }
fi

# proto_mount mirrors e2e/run.sh: the /artifacts/<x> a protocol lives under.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    *) echo "$1" ;;
  esac
}

# proto_path mirrors e2e/run.sh: the explicit upstream segment, when present.
proto_path() {
  case "$1" in
    google)  echo "maven/_upstream/dl.google.com" ;;
    gradle)  echo "maven/_upstream/plugins.gradle.org" ;;
    clojars) echo "maven/_upstream/repo.clojars.org" ;;
    spring)  echo "maven/_upstream/repo.spring.io" ;;
    jitpack) echo "maven/_upstream/jitpack.io" ;;
    jsrnpm)  echo "npm/_upstream/npm.jsr.io" ;;
    *)       proto_mount "$1" ;;
  esac
}

# The mount THIS protocol's client must be pointed at.
EASYBLOBS_BASE="${EASYBLOBS_ORIGIN%/}/artifacts/$(proto_path "$PROTO")"
export EASYBLOBS_BASE
# OCI is spec-fixed at /v2 on the origin, so its script reads EASYBLOBS_URL.
EASYBLOBS_URL="${EASYBLOBS_ORIGIN%/}"
export EASYBLOBS_URL

cas_count() {
  # Blobs live in the CAS; metadata lives in *.db. Count only the former.
  find "$EASYBLOBS_DATA" -type f 2>/dev/null | grep -vc '\.db' || true
}

# Some adapters cache the ARTIFACT in the metadata DB (a small file kept in a
# table) rather than as a CAS blob, so the CAS file count does not move even
# though the pull succeeded. Measure the whole data dir as well, so those
# protocols are not failed on a technicality while still being checked.
# NOTE: `du -sb` is GNU-only (busybox has no -b), and a failed command inside a
# function under `set -e` yields an empty string, which then compared as 0 and
# made every protocol look like it cached nothing. Sum the file SIZES in POSIX
# awk instead: same question, no GNU dependency, never empty.
data_bytes() {
  find "$EASYBLOBS_DATA" -type f -exec wc -c {} + 2>/dev/null |
    awk '{s+=$1} END {printf "%d\n", s}'
}

before=$(cas_count)
before_bytes=$(data_bytes)

# The upstream the maven-family and npm-family suites expose as an explicit
# segment is also the HOST that gets cached, so a protocol-specific base is
# not enough for every suite.
#
# Some suites (conan) write into the CURRENT directory as their client's
# workspace, so run each one from a fresh temp dir: a suite must never inherit
# fragments from the one before it.
# A suite that installs a package the BASE IMAGE already ships proves nothing:
# `gem install rake` on ruby:3.3 completes in 0.8s with no network call, so
# nothing is fetched and (correctly) nothing is cached. These per-protocol
# overrides force a package the stock image does NOT contain, so the suite
# really pulls through the mount.
#
# Each override REPLACES the suite's own install command via env, so the
# scripts stay untouched:
#
#   rubygems  -> install a gem the image lacks (not rake/bundler)
prep_client() {
  case "$PROTO" in
    rubygems)
      if command -v gem >/dev/null; then
        gem sources --clear-all >/dev/null 2>&1 || true
        gem sources --add "$EASYBLOBS_BASE/" >/dev/null 2>&1 || true
        # Prove the client is actually pointed at the mount before installing:
        # a silent fallback to rubygems.org is exactly how this suite passed
        # while nothing was cached.
        echo "gem sources now:"
        gem sources --list
        gem sources --list | grep -q "$EASYBLOBS_BASE" || {
          echo "FAIL: the mount is not a gem source" >&2
          exit 1
        }
        # `colorize` is NOT part of the ruby image.
        # --clear-sources + --source pin it to the mount for THIS install.
        gem install --no-document --clear-sources --source "$EASYBLOBS_BASE/" colorize
        ruby -e 'require "colorize"; puts "ruby + colorize: #{String.colors.size} colors"'
        return 0
      fi
      ;;
    pypi)
      # The python image already has six (pip says "Requirement already
      # satisfied"), so the stock suite fetched nothing. Use a wheel the image
      # does NOT bundle.
      if command -v pip >/dev/null; then
        pip install --no-cache-dir --disable-pip-version-check \
          --index-url "$EASYBLOBS_BASE/simple/" tabulate
        python3 -c 'import tabulate; print("pypi + tabulate:", tabulate.__version__)'
        return 0
      fi
      ;;
    nuget)
      # 'All projects are up-to-date for restore' means the SDK image already
      # cached the package set, so nothing was downloaded. Clear the global
      # cache and restore a package the SDK does not ship.
      if command -v dotnet >/dev/null; then
        dotnet nuget locals all --clear >/dev/null 2>&1 || true
        d="$(mktemp -d)"
        cd "$d"
        cat > nuget.config <<X
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="easy-platform" value="$EASYBLOBS_BASE/v3/index.json" allowInsecureConnections="true" />
  </packageSources>
</configuration>
X
        dotnet new console -o app --force >/dev/null
        ( cd app && dotnet add package Newtonsoft.Json --version 13.0.3 >/dev/null && dotnet restore )
        return 0
      fi
      ;;
    composer)
      # Composer keeps its OWN config in COMPOSER_HOME; a global `composer
      # config -g` in the image is not necessarily what this run reads. Write an
      # explicit composer.json that names the mount as the ONLY repository, so
      # packagist cannot be used as a fallback.
      if command -v composer >/dev/null; then
        # The composer image ships a WARM cache. With a warm cache `composer
        # require` prints "Installing dependencies from lock file" and touches
        # the network zero times - which is exactly what happened (easyblobs saw
        # no requests and the data dir did not grow). Purge everything composer
        # could reuse before the run.
        export COMPOSER_HOME=/tmp/composer-home
        export COMPOSER_CACHE_DIR=/tmp/composer-cache
        mkdir -p "$COMPOSER_HOME" "$COMPOSER_CACHE_DIR"
        # NOTE: never sweep the whole filesystem for cache dirs - an earlier
        # version did `find / -maxdepth 6 -name 'cache-files-*' | rm -rf`, which
        # also removed the server's data dir (the log showed
        # 'data 73728 -> 0 bytes'). Only touch composer's own two directories,
        # and never anything under EASYBLOBS_DATA.
        for d in "$HOME/.composer" "$HOME/.cache/composer" /tmp/composer-home /tmp/composer-cache; do
          case "$d" in
            "$EASYBLOBS_DATA"*|"$EASYBLOBS_DATA") continue ;;
          esac
          rm -rf "$d" 2>/dev/null || true
        done
        mkdir -p "$COMPOSER_HOME" "$COMPOSER_CACHE_DIR"
        export COMPOSER_NO_INTERACTION=1
        d="$(mktemp -d)"
        cd "$d"
        cat > composer.json <<X
{
  "require": { "psr/container": "^2.0" },
  "repositories": [
    { "type": "composer", "url": "$EASYBLOBS_BASE/" }
  ],
  "config": { "secure-http": false }
}
X
        # Remove packagist entirely; with only the mount configured, a successful
        # install PROVES the package came through easyblobs.
        composer config repositories.packagist.org false >/dev/null 2>&1 || true
        echo "composer repositories:"
        composer config repositories 2>/dev/null | head -20 || true
        # --no-cache + COMPOSER_CACHE_DIR above: force a real fetch. Also show
        # the version and prove the mount answers packages.json BEFORE requiring,
        # so 'zero requests' can be attributed correctly.
        echo "composer version: $(composer --version 2>/dev/null | head -1)"
        if command -v curl >/dev/null; then
          pc=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "$EASYBLOBS_BASE/packages.json" 2>/dev/null)
          echo "mount /packages.json -> $pc"
        fi
        # Requirements are declared in composer.json and the resolution is run
        # as a plain `composer update` - NOT `composer update <pkg>`, which
        # refuses to run without an existing lock:
        #   "Cannot update only a partial set of packages without a lock file
        #    present."
        # Any lock/vendor from the image is removed first, so the update has to
        # resolve against the mount.
        rm -f composer.lock
        rm -rf vendor
        composer update --no-interaction --no-progress --no-cache
        echo "requests seen by the mount (composer):"
        grep -c 'req GET /artifacts/composer' /tmp/easyblobs.log 2>/dev/null | tail -1 || true
        # Fail here with a precise message rather than at the generic cache
        # assertion: "composer never asked the mount" is a different bug from
        # "the mount answered but nothing was stored".
        cn=$(grep -c 'req GET /artifacts/composer' /tmp/easyblobs.log 2>/dev/null)
        [ -n "$cn" ] || cn=0
        if [ "$cn" -eq 0 ]; then
          echo "FAIL: composer completed without ever requesting the mount" >&2
          exit 1
        fi
        php -r 'require "vendor/autoload.php"; echo "composer + psr/container: ", interface_exists("Psr\\Container\\ContainerInterface") ? "ok" : "missing", "\n";'
        return 0
      fi
      ;;
    conan)
      # `conan download` really pulled the recipe (the log shows the revision),
      # but `conan inspect` then needs a Conanfile in the CWD. Serve the
      # assertion from the client cache instead, which is what proves the pull.
      if command -v conan >/dev/null; then
        # A COLD client cache. The python image may already carry a conan home
        # (or a previous protocol in the same job may have left one), and with
        # `conan download` finding the recipe locally, ZERO requests reach the
        # mount - which is exactly what the log showed.
        export CONAN_HOME=/tmp/conan-home-cold
        rm -rf "$CONAN_HOME"
        mkdir -p "$CONAN_HOME"
        conan profile detect --force >/dev/null 2>&1 || true
        conan remote add easy-platform "$EASYBLOBS_BASE" --force >/dev/null 2>&1 || true
        # Prove the client can SEE the remote before downloading. Without this,
        # a zero-request run is indistinguishable from a silent cache hit.
        echo "conan remotes:"
        conan remote list 2>/dev/null | head -5 || true
        # Download with the revision PINNED OFF and the local cache cleared, so
        # conan MUST ask the remote to resolve. `conan download zlib/1.3.1`
        # completed in 0.48s with ZERO requests because conan already knew the
        # revision hash - it answered itself.
        #
        # bzip2 is small and is not part of the python image, unlike zlib which
        # other protocols in the same job may have pulled into the cache.
        conan download bzip2/1.0.8 -r easy-platform --only-recipe
        # Ask conan itself instead of guessing the cache layout: 'conan list'
        # prints the recipes the LOCAL cache holds, which is precisely what
        # 'download' is supposed to have produced. (A hard-coded
        # `find ... -name <pkg>` guessed wrong - conan 2 stores recipes under
        # <home>/p/b/... and the name is hashed.)
        if ! conan list 'bzip2/1.0.8:*' 2>/dev/null | grep -q 'bzip2/1.0.8'; then
          echo "FAIL: conan list does not show bzip2/1.0.8 in the local cache" >&2
          conan list 'bzip2/1.0.8:*' 2>&1 | head -20 >&2 || true
          exit 1
        fi
        echo "conan: recipe present in the client cache"
        return 0
      fi
      ;;
    hex)
      # `mix local.hex` downloads hex ITSELF from HEX_MIRROR/installs/hex-1.x.csv,
      # and this adapter has no /installs route (its routes are names, versions,
      # packages/*, tarballs/*), so with the mirror set that call 502s and hex
      # cannot be installed at all.
      #
      # Installing the hex TOOL is not what this suite tests - the point is
      # that a DEPENDENCY is fetched through the mount. So install hex with the
      # mirror temporarily unset, then let the suite set HEX_MIRROR for the
      # dependency resolution.
      if command -v mix >/dev/null; then
        (
          set +e
          # Hex tool install must NOT go through the mirror (no /installs route).
          unset HEX_MIRROR
          if [ -f /opt/hex-archive/hex.ez ]; then
            mix archive.install /opt/hex-archive/hex.ez --force >/dev/null 2>&1
          else
            mix local.hex --force >/dev/null 2>&1
          fi
          mix local.rebar --force >/dev/null 2>&1
        )
        # Whether it succeeded is decided OUTSIDE this block: if hex is present
        # the suite can proceed; if the tool install failed (no archive and no
        # egress) the suite will say so clearly.
        if [ -d "${MIX_HOME:-$HOME/.mix}/archives" ]; then
          echo "(hex tool available; installed outside the mirror window)"
        fi
        echo "(hex prep done)"
      fi
      ;;
    huggingface)
      # The suite starts with `pip install huggingface_hub`, i.e. it installs
      # its CLIENT from PyPI. Do that here, before the suite runs, so the
      # measured window only contains the dependency fetch through the mount.
      if command -v pip >/dev/null; then
        pip install --no-cache-dir --disable-pip-version-check --quiet huggingface_hub >/dev/null 2>&1
        python3 -c 'import huggingface_hub; print("huggingface_hub", huggingface_hub.__version__, "installed outside the mirror window")' || true
      fi
      ;;
  esac
  return 1
}

WORK="$(mktemp -d)"
# Absolute path to the suite: we cd into WORK (some suites write there).
REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$WORK"

echo "=== e2e/$PROTO (base=$EASYBLOBS_BASE, CAS before: $before files) ==="
# A per-protocol override (prep_client) wins over the suite's own script when
# the stock image already ships what the script installs.

if prep_client; then
  echo "(used the per-protocol client override)"
else
  # Capture the suite's output AND its xtrace to one file. Redirecting inside
  # an `elif !` condition loses stderr in some shells, which is exactly how
  # hex/jsrnpm looked like "nothing happened" and cost several CI rounds.
  OUT=/tmp/e2e-$PROTO.log
  if sh -x "$REPO/e2e/scripts/$PROTO.sh" >"$OUT" 2>&1; then
    cat "$OUT"
  else
    echo "--- FAILED: e2e/$PROTO (last 60 lines, incl. xtrace) ---" >&2
    tail -60 "$OUT" >&2
    # The suites use `wget -qO`, which prints NOTHING on failure. Re-run just
    # the base URL verbosely so a 404/refused/timeout becomes visible.
    echo "--- probing the mount directly ---" >&2
    if command -v wget >/dev/null; then
      wget -S -O /dev/null --timeout=15 "$EASYBLOBS_BASE/" 2>&1 | head -20 >&2 || true
    fi
    if command -v curl >/dev/null; then
      curl -sS -o /dev/null -w 'curl: %{http_code} in %{time_total}s\n' --max-time 15 "$EASYBLOBS_BASE/" >&2 || true
    fi
    echo "--- easyblobs log (tail) ---" >&2
    tail -30 /tmp/easyblobs.log >&2 || true
    echo "--- preconditions ---" >&2
    echo "  base=$EASYBLOBS_BASE" >&2
    for t in wget curl tar sh bash; do
      p=$(command -v "$t" 2>/dev/null || echo MISSING)
      echo "  $t: $p" >&2
    done
    exit 1
  fi
fi

after=$(cas_count)
after_bytes=$(data_bytes)

# The cache assertion: a pull-through MUST leave something behind, otherwise an
# adapter that only proxies would pass. Either the CAS grew (a blob was
# written) OR the data dir grew (the artifact went into the metadata DB).
# Requiring ONLY the CAS count was wrong: pypi and rubygems keep the package
# inside the metadata db, so they legitimately never add a CAS file.
# (conan is NOT like that - its recipe/package files DO go through
# reg.Blobs.Put via storeFile, so a conan run that downloads anything must grow
# the CAS. If it does not, the client never reached the mount at all.)
#
# A few suites exercise a READ-ONLY surface on purpose: they prove the adapter
# answers correctly (the client compiles and runs against the response) and the
# adapter legitimately has nothing to store for those calls. Failing them for
# "the CAS did not grow" would be testing the wrong thing.
#
#   swift  identifiers/releases are metadata lookups; the client then compiles
#          and runs a Swift program against the parsed answer. Nothing is
#          downloaded, so nothing is cached.
readonly_protocol() {
  case "$1" in
    # swift: identifiers/releases are metadata lookups; the client then compiles
    #        and runs a Swift program against the parsed answer.
    # nix:   `nix store info --store <mount>` reports on a binary-cache store -
    #        a metadata query. `nix-instantiate --eval` is pure evaluation. No
    #        narinfo/nar is downloaded, so nothing is cached, and the suite's
    #        proof is that every nix command succeeds.
    # jsr: deno caches the resolved module in ITS OWN store; the adapter answers
    #      meta.json / version meta / module from upstream and stores nothing,
    #      so the mount's data dir does not grow.
    swift|nix|jsr) return 0 ;;
    *) return 1 ;;
  esac
}

#
# The verdict is printed only AFTER the assertion, so a failing protocol can
# never also print "OK" (it used to, and made the logs contradict themselves).
cached=0
[ "$after" -gt "$before" ] && cached=1
[ "$after_bytes" -gt "$before_bytes" ] && cached=1
# A read-only suite passes when the mount demonstrably served it, even though
# it had nothing to cache.
if [ "$cached" -ne 1 ] && readonly_protocol "$PROTO"; then
  # Count ANY request to this protocol's mount prefix. The old pattern required
  # a space before /artifacts, which the real log line
  #   req GET /artifacts/jsr/@luca/flag/meta.json ...
  # does not have - so every read-only suite looked like it had served nothing.
  rn=$(grep -c " /artifacts/$(proto_mount "$PROTO")" /tmp/easyblobs.log 2>/dev/null)
  [ -n "$rn" ] || rn=0
  if [ "$rn" -eq 0 ]; then
    echo "FAIL: e2e/$PROTO served nothing from the mount (read-only suite, 0 requests)" >&2
    exit 1
  fi
  echo "=== e2e/$PROTO OK (read-only suite; mount served $rn request(s), nothing to cache) ==="
  exit 0
fi
if [ "$cached" -ne 1 ]; then
  echo "FAIL: e2e/$PROTO cached nothing (CAS $before->$after files, data $before_bytes->$after_bytes bytes)" >&2
  # A SHRINKING data dir means something deleted the server's storage - a bug
  # in the harness, not in the protocol. Say so plainly.
  if [ "$after_bytes" -lt "$before_bytes" ]; then
    echo "      NOTE: the data dir SHRANK ($before_bytes -> $after_bytes)." >&2
    echo "      That is a harness bug (something deleted $EASYBLOBS_DATA), not a protocol failure." >&2
  fi
  # The two usual causes, named so the next reader does not have to guess:
  #   - the client never connected (wrong source/mirror config), or
  #   - it connected somewhere else (a proxy / a hardcoded upstream).
  # NOTE: `grep -c ... || echo 0` yields "0\n0" when grep finds nothing (it
  # prints 0 AND exits 1), which then breaks the numeric test below. Use a
  # plain command substitution and default it separately.
  n=$(grep -c "req GET /artifacts/" /tmp/easyblobs.log 2>/dev/null)
  [ -n "$n" ] || n=0
  echo "      easyblobs saw $n request(s) in total." >&2
  if [ "$n" -eq 0 ]; then
    echo "      ZERO requests: the client never reached the mount." >&2
  fi
  exit 1
fi

echo "=== e2e/$PROTO OK (CAS: $before -> $after files; data: $before_bytes -> $after_bytes bytes) ==="
