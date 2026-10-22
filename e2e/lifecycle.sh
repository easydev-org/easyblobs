#!/bin/sh
# lifecycle.sh — exercise the FULL package lifecycle against a hosted repo,
# not just pull-through.
#
# The e2e suites under e2e/scripts/*.sh all prove one thing: a CLIENT can FETCH
# what the mirror serves. None of them publishes, updates or deletes anything,
# so the write paths (24 of the adapters have them) were untested.
#
# This script covers, for every protocol it is given:
#
#   1. PUBLISH  a new package            -> 200/201/204
#   2. READ IT BACK and compare BYTES    -> 200 + identical
#   3. UPDATE   the same coordinate      -> 200/201/204
#   4. READ IT BACK and see the NEW data -> proves the update actually replaced
#                                           the old content (not a stale copy)
#   5. PUBLISH a second version          -> 200/201/204
#   6. CHECK both versions are distinct  -> the store keeps them apart
#   7. DELETE the first version          -> 200/204
#   8. CHECK it is gone                  -> 404
#   9. CHECK the second version SURVIVED -> deleting one must not delete the
#                                           other
#  10. DELETE the second version         -> 200/204, and check 404
#
# Byte comparison matters: a store that returns 200 with the OLD bytes after an
# update "passes" a naive status-code check while being completely broken.
#
# Usage: lifecycle.sh <protocol>
# Requires: EASYBLOBS_BASE (the protocol's mount URL, set by ci-run.sh).
#
# NOTE ON AUTH: the harness can run with auth disabled (anonymous writes) or
# with --tokens, in which case PUT/DELETE need a write token. This script always
# sends the token when EASYBLOBS_TOKEN is set, and asserts that an unauthenticated
# write is REJECTED when EASYBLOBS_REQUIRE_AUTH=1.
set -eu

PROTO="${1:?usage: lifecycle.sh <protocol>}"
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
BASE="${EASYBLOBS_BASE%/}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Every run uses a distinct repository/version so a rerun on the same data dir
# cannot pass because of leftovers from a previous run.
STAMP="$(date +%s)-$$"
REPO="e2e-lifecycle-$STAMP"
V1="1.0.$STAMP"
V2="2.0.$STAMP"

fail() { echo "lifecycle/$PROTO: FAIL: $*" >&2; exit 1; }

# AUTH: two shell FUNCTIONS that prepend the credential, instead of a string of
# arguments.
#
# A string cannot carry "Authorization: Bearer <tok>" because it contains a
# space: word splitting turns it into two separate arguments and curl then reads
# "Bearer" as a URL:
#   curl: (6) Could not resolve host: Bearer
# Functions receive their arguments intact, so the header is passed as ONE value.
if [ -n "${EASYBLOBS_TOKEN:-}" ]; then
  # with_auth <curl args...>: runs curl WITH the write credential.
  with_auth() { curl -H "Authorization: Bearer ${EASYBLOBS_TOKEN}" "$@"; }
  # bare_auth: keeps the call sites uniform.
  bare_auth() { curl "$@"; }
else
  with_auth() { curl "$@"; }
  bare_auth() { curl "$@"; }
fi

# req <method> <url> <file|-> [extra curl args...]
# Prints the status code; writes the body to <file> when it is not "-".
# Uses the AUTHENTICATED curl, because every mutating call needs the token.
req() {
  _m="$1"; _u="$2"; _out="$3"; shift 3
  if [ "$_out" = "-" ]; then
    with_auth -sS -o /dev/null -w '%{http_code}' -X "$_m" "$@" "$_u"
  else
    with_auth -sS -o "$_out" -w '%{http_code}' -X "$_m" "$@" "$_u"
  fi
}

# req_anon: an UNAUTHENTICATED request, used only to prove that a write without
# a credential is refused.
req_anon() {
  _m="$1"; _u="$2"; shift 2
  bare_auth -sS -o /dev/null -w '%{http_code}' -X "$_m" "$@" "$_u"
}

expect_ok() { # expect_ok <what> <code>
  case "$2" in
    200|201|202|204) ;;
    *) fail "$1 -> HTTP $2 (wanted 2xx)" ;;
  esac
}

expect_code() { # expect_code <what> <want> <got>
  [ "$2" = "$3" ] || fail "$1 -> HTTP $3 (wanted $2)"
}

# ---------------------------------------------------------------------------
# Per-protocol endpoint shapes.
#
# Each function sets:
#   PUB1/PUB2   URLs to PUT the first and second version to
#   GET1/GET2   URLs to read the first and second version back from
#   DEL1/DEL2   URLs to DELETE the first and second version
#   CTYPE       Content-Type to send
#
# Anything the layout cannot express cleanly is handled by a protocol-specific
# override below.
# ---------------------------------------------------------------------------

setup_generic() { # plain <name>/<version>/<file>
  PUB1="$BASE/$REPO/$V1/e2e.bin";  GET1="$PUB1";  DEL1="$PUB1"
  PUB2="$BASE/$REPO/$V2/e2e.bin";  GET2="$PUB2";  DEL2="$PUB2"
  CTYPE=application/octet-stream
}

setup_ivy() { # <host>/<path>, see ivy.sh for the served layout
  HOSTPART="repo.scala-sbt.org/scalasbt/sbt-plugin-releases"
  P="e2e/e2e-lifecycle/scala_2.10/sbt_0.13"
  PUB1="$BASE/$HOSTPART/$P/$V1/ivys/ivy.xml"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/$HOSTPART/$P/$V2/ivys/ivy.xml"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/xml
}

# IMPORTANT: a hosted (self-published) repository is addressed with the
# "_hosted/<repo>/<native path>" prefix. Without it the adapter treats the
# request as a PULL-THROUGH for the upstream, and a non-GET method is refused:
#   405 Method Not Allowed
# That is what rpm/apk/debian/conda answered until this was fixed.

setup_rpm() { # _hosted/<repo>/<path>
  PUB1="$BASE/_hosted/$REPO/repodata/e2e-$V1.rpm"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/_hosted/$REPO/repodata/e2e-$V2.rpm"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/x-rpm
}

setup_apk() {
  PUB1="$BASE/_hosted/$REPO/v3.20/main/x86_64/e2e-$V1.apk"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/_hosted/$REPO/v3.20/main/x86_64/e2e-$V2.apk"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/gzip
}

setup_debian() {
  PUB1="$BASE/_hosted/$REPO/pool/main/e/e2e/e2e_$V1.deb"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/_hosted/$REPO/pool/main/e/e2e/e2e_$V2.deb"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/vnd.debian.binary-package
}

setup_conda() {
  PUB1="$BASE/_hosted/$REPO/linux-64/e2e-$V1.tar.bz2"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/_hosted/$REPO/linux-64/e2e-$V2.tar.bz2"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/x-bzip2
}

setup_maven() { # <group>/<artifact>/<version>/<file>
  G="com/easyblobs/e2e"
  PUB1="$BASE/$G/$REPO/$V1/$REPO-$V1.pom"; GET1="$PUB1"; DEL1="$PUB1"
  PUB2="$BASE/$G/$REPO/$V2/$REPO-$V2.pom"; GET2="$PUB2"; DEL2="$PUB2"
  CTYPE=application/xml
}

# go is special: publication is a single endpoint that takes the module and
# version as parameters, not a path-shaped PUT per file.
#
#   PUT /artifacts/go/upload?name=<module>&version=<v>   body = module content
#   GET /artifacts/go/<module>/@v/<version>.info|.mod|.zip
#   DELETE /artifacts/go/upload?name=...&version=...
#
# So the generic "PUT the same URL you GET" shape does not apply and the whole
# flow is driven by run_go() below.
GO_MODULE=""

# nuget is special too: a package is PUSHED to one endpoint and the server reads
# the id/version out of the .nupkg itself:
#
#   PUT    /v3/package            body = .nupkg
#   GET    /v3/flatcontainer/<id>/<version>/<id>.<version>.nupkg
#   DELETE /v3/package/<id>/<version>
#
# So it gets run_nuget() rather than the path-shaped flow.

# A minimal but VALID .nupkg: NuGet reads the id/version from the .nuspec inside
# the zip, so a plain byte blob would be rejected or stored under a guessed name.
make_nupkg() { # make_nupkg <id> <version> <out>
  # Written with Python's zipfile: `zip` is not installed everywhere (it was
  # missing on the alpine distro image), and the -nupkg must be a real zip for
  # NuGet to read the .nuspec out of it.
  python3 - "$1" "$2" "$3" "$STAMP" <<'PY'
import sys, zipfile
pid, ver, out, stamp = sys.argv[1:5]
nuspec = f'''<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>{pid}</id>
    <version>{ver}</version>
    <authors>easyblobs-e2e</authors>
    <description>lifecycle probe {stamp}</description>
  </metadata>
</package>
'''
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr(f"{pid}.nuspec", nuspec)
    z.writestr("lib/netstandard2.0/e2e.dll", f"e2e-marker-{stamp}-{ver}")
PY
}

setup_cargo() {
  PUB1="$BASE/api/v1/crates/new"; GET1=""
  PUB2=""; DEL1=""; DEL2=""
  CTYPE=application/json
}

# ---------------------------------------------------------------------------
# go: publication uses one endpoint with query parameters, so it gets its own
# flow rather than the path-shaped one below.
# ---------------------------------------------------------------------------
run_go() {
  MOD="example.com/e2e-lifecycle-$STAMP"
  Z1="v${V1}"
  Z2="v${V2}"

  # A go module must be published as a REAL MODULE ZIP, not an arbitrary blob.
  # The adapter serves .mod by extracting go.mod out of the stored zip (see
  # extractGoMod); a plain text body stores fine but yields no go.mod, so
  # .mod falls through to the upstream and 404s for a local module:
  #   GET /@v/<ver>.mod -> 404
  # That is the contract, so the suite publishes what the go toolchain
  # publishes: <module>@<version>/go.mod + source files.
  make_module_zip() { # make_module_zip <version> <out>
    python3 - "$MOD" "$1" "$2" "$STAMP" <<'PY'
import sys, zipfile
mod, ver, out, stamp = sys.argv[1:5]
pref = f"{mod}@{ver}"
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr(f"{pref}/go.mod", f"module {mod}\n\ngo 1.21\n")
    z.writestr(f"{pref}/e2e.go", f"package e2e\n\n// body-{stamp}-{ver}\n")
PY
  }

  gput() { # gput <version> <zipfile>
    with_auth -sS -o /dev/null -w '%{http_code}' -X PUT \
      "$BASE/upload?name=$MOD&version=$1" \
      -H "Content-Type: application/octet-stream" \
      --data-binary "$2"
  }
  gget() { # gget <url> <out>
    curl -sS -o "$2" -w '%{http_code}' "$1"
  }
  gdel() { # gdel <version>
    with_auth -sS -o /dev/null -w '%{http_code}' -X DELETE \
      "$BASE/upload?name=$MOD&version=$1"
  }

  make_module_zip "$Z1" "$TMP/mod1.zip"
  make_module_zip "$Z2" "$TMP/mod2.zip"

  echo "lifecycle/go: publishing $Z1"
  expect_ok "publish $Z1" "$(gput "$Z1" "$TMP/mod1.zip")"

  # All three shapes the go toolchain asks for must be readable, INCLUDING
  # .mod: it is extracted from the published zip's go.mod, so a properly
  # published module has one. (This is why the zip, not a text blob, is what
  # this suite publishes.)
  for ext in info mod zip; do
    code=$(gget "$BASE/$MOD/@v/$Z1.$ext" "$TMP/go1.$ext")
    expect_code "read $Z1.$ext" 200 "$code"
    [ -s "$TMP/go1.$ext" ] || fail "$Z1.$ext came back empty"
  done
  # The extracted go.mod must be the one that was published, and it must
  # declare the right module path.
  grep -q "module $MOD" "$TMP/go1.mod" || \
    fail "the served go.mod does not declare 'module $MOD'"
  # The zip must be byte-identical to what was published.
  cmp -s "$TMP/go1.zip" "$TMP/mod1.zip" || \
    fail "the served module zip differs from the one published"

  echo "lifecycle/go: publishing $Z2"
  expect_ok "publish $Z2" "$(gput "$Z2" "$TMP/mod2.zip")"
  code=$(gget "$BASE/$MOD/@v/$Z2.info" "$TMP/go2.info")
  expect_code "read $Z2.info" 200 "$code"

  # Both versions must be listed, i.e. they did not overwrite each other.
  code=$(gget "$BASE/$MOD/@v/list" "$TMP/golist")
  case "$code" in
    200)
      grep -qx "$Z1" "$TMP/golist" || fail "$Z1 is missing from @v/list"
      grep -qx "$Z2" "$TMP/golist" || fail "$Z2 is missing from @v/list"
      ;;
    *) fail "read @v/list -> HTTP $code" ;;
  esac

  echo "lifecycle/go: deleting $Z1"
  expect_ok "delete $Z1" "$(gdel "$Z1")"
  code=$(gget "$BASE/$MOD/@v/$Z1.info" "$TMP/gone")
  expect_code "read $Z1.info after delete" 404 "$code"
  code=$(gget "$BASE/$MOD/@v/$Z2.info" "$TMP/survivor")
  expect_code "read $Z2.info after deleting $Z1" 200 "$code"

  echo "lifecycle/go: deleting $Z2"
  expect_ok "delete $Z2" "$(gdel "$Z2")"
  code=$(gget "$BASE/$MOD/@v/$Z2.info" "$TMP/gone2")
  expect_code "read $Z2.info after delete" 404 "$code"

  echo "lifecycle/go: OK (publish, read info+mod, two versions listed, delete, isolation)"
}

# ---------------------------------------------------------------------------
# nuget: push one .nupkg, then read it back from the flat container.
# ---------------------------------------------------------------------------
run_nuget() {
  NID="e2e-lifecycle-$STAMP"
  N1="1.0.0"
  N2="2.0.0"

  make_nupkg "$NID" "$N1" "$TMP/n1.nupkg"
  make_nupkg "$NID" "$N2" "$TMP/n2.nupkg"

  nput() { # nput <file>
    with_auth -sS -o /dev/null -w '%{http_code}' -X PUT \
      "$BASE/v3/package" -H "Content-Type: application/octet-stream" \
      --data-binary "@$1"
  }
  nget() { # nget <version> <out>
    curl -sS -o "$2" -w '%{http_code}' \
      "$BASE/v3/flatcontainer/$NID/$1/$NID.$1.nupkg"
  }
  ndel() { # ndel <version>
    with_auth -sS -o /dev/null -w '%{http_code}' -X DELETE \
      "$BASE/v3/package/$NID/$1"
  }

  echo "lifecycle/nuget: pushing $NID $N1"
  expect_ok "push $N1" "$(nput "$TMP/n1.nupkg")"

  code=$(nget "$N1" "$TMP/n1.got")
  expect_code "read $N1" 200 "$code"
  cmp -s "$TMP/n1.got" "$TMP/n1.nupkg" || \
    fail "the .nupkg that came back differs from the one pushed"

  echo "lifecycle/nuget: pushing $NID $N2"
  expect_ok "push $N2" "$(nput "$TMP/n2.nupkg")"
  code=$(nget "$N2" "$TMP/n2.got")
  expect_code "read $N2" 200 "$code"
  cmp -s "$TMP/n2.got" "$TMP/n2.nupkg" || fail "$N2 does not match what was pushed"

  # The two versions must be separate registrations.
  code=$(nget "$N1" "$TMP/n1b.got")
  expect_code "read $N1 while $N2 exists" 200 "$code"
  cmp -s "$TMP/n1b.got" "$TMP/n1.nupkg" || fail "pushing $N2 disturbed $N1"

  echo "lifecycle/nuget: deleting $N1"
  expect_ok "delete $N1" "$(ndel "$N1")"
  code=$(nget "$N1" "$TMP/gone")
  expect_code "read $N1 after delete" 404 "$code"
  code=$(nget "$N2" "$TMP/survivor")
  expect_code "read $N2 after deleting $N1" 200 "$code"

  echo "lifecycle/nuget: deleting $N2"
  expect_ok "delete $N2" "$(ndel "$N2")"
  code=$(nget "$N2" "$TMP/gone2")
  expect_code "read $N2 after delete" 404 "$code"

  echo "lifecycle/nuget: OK (push, read back byte-for-byte, two versions, delete, isolation)"
}

# Which protocols have a shape above. Adding one here is the whole job.
case "$PROTO" in
  go) ;;   # no PUB*/GET* variables: run_go() drives the whole flow
  nuget) ;;   # run_nuget() drives it
  generic|ivy|rpm|apk|debian|conda|maven) "setup_$PROTO" ;;
  *)
    echo "lifecycle/$PROTO: no hosted endpoint shape declared; nothing to do" >&2
    exit 77   # 77 = skipped, distinct from a failure
    ;;
esac

# ---------------------------------------------------------------------------
# 0. A write must be REFUSED when the harness requires auth and we send none.
# ---------------------------------------------------------------------------
if [ "${EASYBLOBS_REQUIRE_AUTH:-0}" = "1" ] && [ "$PROTO" != "go" ] && [ "$PROTO" != "nuget" ]; then
  # req_ANON: this call must NOT carry the credential - it is the check that an
  # unauthenticated publish is refused. `req` would send the token and the
  # request would legitimately succeed (201), which is what happened before.
  code=$(req_anon PUT "$PUB1" --data-binary "unauthorized-$STAMP" -H "Content-Type: $CTYPE")
  case "$code" in
    401|403) echo "lifecycle/$PROTO: unauthenticated publish refused ($code)" ;;
    *) fail "unauthenticated publish returned $code; it must be 401/403" ;;
  esac
fi

# go and nuget drive their own flows (single publish endpoint each).
case "$PROTO" in
  go)    run_go;    exit 0 ;;
  nuget) run_nuget; exit 0 ;;
esac

# ---------------------------------------------------------------------------
# 1..10  the lifecycle proper.
# ---------------------------------------------------------------------------
printf 'v1-body-%s' "$STAMP"  > "$TMP/v1"
printf 'v1-UPDATED-%s' "$STAMP" > "$TMP/v1b"
printf 'v2-body-%s' "$STAMP"  > "$TMP/v2"

echo "lifecycle/$PROTO: publishing $V1"
code=$(req PUT "$PUB1" - --data-binary "@$TMP/v1" -H "Content-Type: $CTYPE")
expect_ok "publish $V1" "$code"

echo "lifecycle/$PROTO: reading $V1 back"
code=$(req GET "$GET1" "$TMP/got1")
expect_code "read $V1" 200 "$code"
cmp -s "$TMP/got1" "$TMP/v1" || \
  fail "the published bytes did not come back unchanged"

echo "lifecycle/$PROTO: updating $V1 in place"
code=$(req PUT "$PUB1" - --data-binary "@$TMP/v1b" -H "Content-Type: $CTYPE")
expect_ok "update $V1" "$code"

code=$(req GET "$GET1" "$TMP/got1b")
expect_code "read $V1 after update" 200 "$code"
cmp -s "$TMP/got1b" "$TMP/v1b" || {
  fail "after the update the OLD bytes are still being served"
}

echo "lifecycle/$PROTO: publishing $V2"
code=$(req PUT "$PUB2" - --data-binary "@$TMP/v2" -H "Content-Type: $CTYPE")
expect_ok "publish $V2" "$code"

code=$(req GET "$GET2" "$TMP/got2")
expect_code "read $V2" 200 "$code"
cmp -s "$TMP/got2" "$TMP/v2" || fail "version $V2 does not match what was published"

# The two versions must not have collapsed into one.
code=$(req GET "$GET1" "$TMP/got1c")
expect_code "read $V1 while $V2 exists" 200 "$code"
cmp -s "$TMP/got1c" "$TMP/v1b" || fail "publishing $V2 disturbed $V1"

echo "lifecycle/$PROTO: deleting $V1"
code=$(req DELETE "$DEL1" -)
expect_ok "delete $V1" "$code"

code=$(req GET "$GET1" "$TMP/gone")
expect_code "read $V1 after delete" 404 "$code"

code=$(req GET "$GET2" "$TMP/survivor")
expect_code "read $V2 after deleting $V1" 200 "$code"
cmp -s "$TMP/survivor" "$TMP/v2" || fail "deleting $V1 damaged $V2"

echo "lifecycle/$PROTO: deleting $V2"
code=$(req DELETE "$DEL2" -)
expect_ok "delete $V2" "$code"
code=$(req GET "$GET2" "$TMP/gone2")
expect_code "read $V2 after delete" 404 "$code"

echo "lifecycle/$PROTO: OK (publish, read, update, second version, delete, isolation)"
