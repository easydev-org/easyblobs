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

# ISOLATION. This is the single most important thing in this harness: if the
# client can reach the real registry it will, and the test passes without
# easyblobs being exercised. That is exactly what happened to rubygems and
# composer: "Successfully installed" while the data dir stayed at the empty-db
# size of 73728 bytes and easyblobs logged ZERO requests.
#
# Blanking the proxy variables is NOT enough on a runner with egress. Instead
# point the client at the ALLOWLIST PROXY (started by the workflow), which
# forwards only the mount and refuses everything else - including every
# CONNECT, so no TLS session to a public registry can be established.
if [ -n "${EASYBLOBS_ALLOWLIST_PROXY:-}" ]; then
  export http_proxy="$EASYBLOBS_ALLOWLIST_PROXY"
  export https_proxy="$EASYBLOBS_ALLOWLIST_PROXY"
  export HTTP_PROXY="$EASYBLOBS_ALLOWLIST_PROXY"
  export HTTPS_PROXY="$EASYBLOBS_ALLOWLIST_PROXY"
  unset all_proxy ALL_PROXY
  # The mount itself must NEVER go through the proxy (avoids a loop and keeps
  # the fast path direct); everything else has no choice but the proxy.
  export NO_PROXY="127.0.0.1,localhost"
  export no_proxy="$NO_PROXY"
  echo "(egress restricted to the mount via $EASYBLOBS_ALLOWLIST_PROXY)"
else
  # No proxy configured: keep the earlier best-effort blanking, and be loud.
  unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY all_proxy ALL_PROXY
  export NO_PROXY="*"
  export no_proxy="*"
  echo "WARNING: no allowlist proxy; a client with direct egress can bypass the mount." >&2
fi
# A few clients consult their own mirror variables; point them at the mount.
export GOPROXY="$EASYBLOBS_ORIGIN/artifacts/go"
export GOSUMDB=off

# ---------------------------------------------------------------- proof -----
# Before any protocol runs, prove that the mount is reachable AND that the
# public registry is NOT. If the public registry answers, the client will
# prefer it and the test proves nothing (that is exactly how rubygems and
# composer "passed" while easyblobs saw zero requests).
echo "--- connectivity sentinel ---"
if command -v curl >/dev/null; then
  code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 "$EASYBLOBS_ORIGIN/healthz" || echo 000)
  echo "  mount /healthz      -> $code"
  [ "$code" = "200" ] || { echo "FATAL: the mount is not reachable" >&2; exit 1; }
  pub=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 8 https://rubygems.org/ 2>/dev/null || echo 000)
  echo "  rubygems.org        -> $pub"
  pub2=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 8 https://repo.packagist.org/packages.json 2>/dev/null || echo 000)
  echo "  packagist.org       -> $pub2"
  # Not fatal (a runner may legitimately have no egress), but loud: with egress
  # a client CAN bypass the mount, so a green result must be read carefully.
  case "$pub$pub2" in
    *200*|*30*) echo "  WARNING: public registries are reachable; the client could bypass the mount." >&2 ;;
  esac
fi

# proto_mount mirrors e2e/run.sh: the /artifacts/<x> a protocol lives under.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
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
        # A warm image cache would satisfy the install without any fetch; drop
        # it so the only place left to get the package is the mount.
        rm -rf "${COMPOSER_HOME:-$HOME/.composer}/cache" "${COMPOSER_HOME:-$HOME/.composer}/cache-files" 2>/dev/null || true
        export COMPOSER_CACHE_DIR="$(mktemp -d)"
        export COMPOSER_NO_INTERACTION=1
        d="$(mktemp -d)"
        cd "$d"
        cat > composer.json <<X
{
  "repositories": [
    { "type": "composer", "url": "$EASYBLOBS_BASE/" }
  ],
  "repositories-priority": ["easy-platform"],
  "config": { "secure-http": false }
}
X
        # Remove packagist entirely; with only the mount configured, a successful
        # install PROVES the package came through easyblobs.
        composer config repositories.packagist.org false >/dev/null 2>&1 || true
        echo "composer repositories:"
        composer config repositories 2>/dev/null | head -20 || true
        composer require --no-interaction --no-progress psr/container
        php -r 'require "vendor/autoload.php"; echo "composer + psr/container: ", interface_exists("Psr\\Container\\ContainerInterface") ? "ok" : "missing", "\n";'
        return 0
      fi
      ;;
    conan)
      # `conan download` really pulled the recipe (the log shows the revision),
      # but `conan inspect` then needs a Conanfile in the CWD. Serve the
      # assertion from the client cache instead, which is what proves the pull.
      if command -v conan >/dev/null; then
        export CONAN_HOME="${CONAN_HOME:-$HOME/.conan2}"
        conan profile detect --force >/dev/null 2>&1 || true
        conan remote add easy-platform "$EASYBLOBS_BASE" --force >/dev/null 2>&1 || true
        conan download zlib/1.3.1 -r easy-platform --only-recipe
        # The recipe dir must now exist in the local cache: that IS the pull.
        find "$CONAN_HOME" -maxdepth 6 -type d -name 'zlib' | head -1 | grep -q . || exit 1
        echo "conan: recipe present in the client cache"
        return 0
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
# Requiring ONLY the CAS count was wrong: pypi/rubygems/conan store the
# package inside pkglab.db, so they legitimately never add a CAS file.
#
# The verdict is printed only AFTER the assertion, so a failing protocol can
# never also print "OK" (it used to, and made the logs contradict themselves).
cached=0
[ "$after" -gt "$before" ] && cached=1
[ "$after_bytes" -gt "$before_bytes" ] && cached=1
if [ "$cached" -ne 1 ]; then
  echo "FAIL: e2e/$PROTO cached nothing (CAS $before->$after files, data $before_bytes->$after_bytes bytes)" >&2
  # The two usual causes, named so the next reader does not have to guess:
  #   - the client never connected (wrong source/mirror config), or
  #   - it connected somewhere else (a proxy / a hardcoded upstream).
  n=$(grep -c "req .* /artifacts/" /tmp/easyblobs.log 2>/dev/null || echo 0)
  echo "      easyblobs saw $n request(s) in total." >&2
  if [ "$n" -eq 0 ]; then
    echo "      ZERO requests: the client never reached the mount." >&2
  fi
  exit 1
fi

echo "=== e2e/$PROTO OK (CAS: $before -> $after files; data: $before_bytes -> $after_bytes bytes) ==="
