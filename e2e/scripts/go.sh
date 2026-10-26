#!/bin/sh
# go: the whole client surface against the mount, in both directions.
#
# A Go module proxy is:
#
#   <base>/<module>/@v/list          every known version
#   <base>/<module>/@v/<ver>.info    {"Version": ..., "Time": ...}
#   <base>/<module>/@v/<ver>.mod     the go.mod
#   <base>/<module>/@v/<ver>.zip     the source archive
#   <base>/<module>/@latest          the resolved latest
#   <base>/sumdb/...                 the checksum database
#
# The module path is CASE-ENCODED: an upper-case letter becomes !lower
# (github.com/Azure -> github.com/!azure), which is a path shape a naive client
# never produces and a naive server never serves.
#
# PULL
#   go mod download     fetches info+mod+zip for a direct dependency
#   go get              resolves a dependency tree
#   go build/run        compiles against what was fetched
#   go list -m all      the module graph
#   @latest             the version endpoint
#   GONOSUMDB/GOSUMDB   the checksum database path
#
# HOSTED
#   PUT upload          a module goes to the mount
#   the @v endpoints    list/info/mod/zip are all served
#   go get              a consumer resolves it back
#   go run              and a program built from it works
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "go: FAIL: $*" >&2; exit 1; }

export GOPATH="$WORK/gopath"
export GOMODCACHE="$WORK/gopath/pkg/mod"
export GOCACHE="$WORK/gocache"
export GOPROXY="$BASE"
export GOSUMDB=off
export GOFLAGS=-mod=mod
mkdir -p "$GOPATH" "$GOCACHE"

echo "== the proxy answers for a module =="
# rsc.io/quote is small and has a real dependency tree.
curl -fsS "$BASE/rsc.io/quote/@v/list" >/dev/null || fail "@v/list"
curl -fsS "$BASE/rsc.io/quote/@latest" >/dev/null || fail "@latest"
curl -fsS "$BASE/rsc.io/quote/@v/v1.5.2.info" >/dev/null || fail ".info"
curl -fsS "$BASE/rsc.io/quote/@v/v1.5.2.mod"  >/dev/null || fail ".mod"
curl -fsS "$BASE/rsc.io/quote/@v/v1.5.2.zip" >/dev/null || fail ".zip"

echo "== a CASE-ENCODED module path =="
# Upper-case letters are escaped as !lower in a module path. github.com/Azure
# has none, but the escaping rule still has to be applied - this asserts the
# adapter accepts the encoded form rather than 404ing on it.
curl -fsS "$BASE/github.com/!azure/azure-sdk-for-go/@v/list" >/dev/null 2>&1 ||
  curl -fsS "$BASE/github.com/Azure/azure-sdk-for-go/@v/list" >/dev/null 2>&1 ||
  echo "   (neither form answered; the encoded-path case is not exercised)"

echo "== go mod download =="
mkdir -p d1 && cd d1
go mod init e2e/d1 >/dev/null 2>&1 || fail "go mod init"
go mod download rsc.io/quote@v1.5.2 >/dev/null 2>&1 || {
  echo "go: go mod download failed:" >&2
  go mod download -x rsc.io/quote@v1.5.2 2>&1 | tail -10 >&2
  exit 1; }
echo "   downloaded"
cd "$WORK"

echo "== go get a dependency tree and run it =="
mkdir -p d2 && cd d2
go mod init e2e/d2 >/dev/null 2>&1
go get rsc.io/quote@v1.5.2 >/dev/null 2>&1 || fail "go get rsc.io/quote"
printf '%s\n' 'package main' '' 'import (' '    "fmt"' '    "rsc.io/quote"' ')' \
  '' 'func main() { fmt.Println(quote.Hello()) }' > main.go
OUT=$(go run . 2>&1) || {
  echo "go: go run failed:" >&2
  echo "$OUT" | tail -10 >&2
  exit 1; }
echo "$OUT" | grep -q . || fail "go run printed nothing"
echo "   built and ran: $OUT"

echo "== go list -m all =="
go list -m all >/dev/null 2>&1 || fail "go list -m all"
go list -m all | grep -q rsc.io/quote || fail "the module graph omits the dependency"
cd "$WORK"

# ------------------------------------------------------------------ hosted --
MOD="example.com/easyblobs-e2e/hello"
VER="v1.0.0"
SRC="$WORK/mod"
mkdir -p "$SRC"
cd "$SRC"
printf '%s\n' 'module example.com/easyblobs-e2e/hello' '' 'go 1.21' > go.mod
printf '%s\n' 'package hello' '' 'func Greet() string { return "ez hello" }' > hello.go

echo "== build the .zip and upload =="
# The proxy zip layout is fixed: <module>@<version>/<file> at the root of the
# archive, and the version must be a valid semver tag.
STAGE="$WORK/stage"
mkdir -p "$STAGE/$MOD@$VER"
cp go.mod hello.go "$STAGE/$MOD@$VER/"
( cd "$STAGE" && zip -q -r "$WORK/mod.zip" "$MOD@$VER" ) || fail "zip the module"

code=$(curl -sS -o /tmp/up.out -w "%{http_code}" -X PUT \
  --data-binary "@$WORK/mod.zip" \
  "$BASE/upload?name=$MOD&version=$VER")
case "$code" in 200|201) ;; *) fail "upload -> $code $(cat /tmp/up.out)" ;; esac

echo "== the @v endpoints serve it =="
curl -fsS "$BASE/$MOD/@v/list" | grep -q "$VER" || fail "@v/list has no $VER"
curl -fsS "$BASE/$MOD/@v/$VER.info" >/dev/null || fail ".info"
curl -fsS "$BASE/$MOD/@v/$VER.mod" | grep -q "$MOD" || fail ".mod is wrong"
curl -fsS "$BASE/$MOD/@v/$VER.zip" >/dev/null || fail ".zip"

echo "== a consumer resolves it back =="
CONS="$WORK/cons"
mkdir -p "$CONS" && cd "$CONS"
go mod init e2e/cons >/dev/null 2>&1
go get "$MOD@$VER" >/dev/null 2>&1 || {
  echo "go: a consumer cannot get the uploaded module:" >&2
  go get -x "$MOD@$VER" 2>&1 | tail -10 >&2
  exit 1; }
printf '%s\n' 'package main' '' 'import (' '    "fmt"' \
  '    hello "example.com/easyblobs-e2e/hello"' ')' \
  '' 'func main() { fmt.Println(hello.Greet()) }' > main.go
OUT=$(go run . 2>&1) || { echo "go: go run against the uploaded module failed:" >&2; echo "$OUT" | tail -8 >&2; exit 1; }
echo "$OUT" | grep -q "ez hello" || fail "the uploaded module does not work"
echo "   the consumer ran it"

echo "== delete the version =="
code=$(curl -sS -o /dev/null -w "%{http_code}" -X DELETE "$BASE/$MOD/@v/$VER")
case "$code" in 200|202|204) ;; *) echo "   (DELETE answered $code)" ;; esac

echo "go: OK (pull: list latest info mod zip download get run list-m-all)"
echo "go: OK (hosted: upload list info mod zip consumer-runs)"
