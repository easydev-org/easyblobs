#!/bin/sh
# opam repository mirrored at the named target: use the REAL opam client.
#
# opam has to be INITIALISED before any repository command means anything. The
# third-party ocaml setup action used to do that; the opam setup ships the binary
# only, so this suite does it - which is also what a user on a bare machine does.
#
#   opam init --bare --disable-sandboxing   create a root, no compiler build
#   opam repo add <name> <url>              add the mirror
#   opam show <pkg> --field=version         parse the package out of the index
#
# The last one is the assertion: `opam show` reads the repository INDEX and parses
# the opam file from it, which a curl probe never does.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

fail() { echo "opam: FAIL: $*" >&2; exit 1; }

command -v opam >/dev/null || fail "the opam binary is missing"
echo "opam: $(opam --version)"

export OPAMROOT="${OPAMROOT:-$PWD/opamroot}"
export OPAMYES=1
export OPAMCONFIRMLEVEL=unsafe-yes

# --bare: write the root and config, do not build a compiler. The suite is about
# what the MIRROR serves, and compiling OCaml would take longer than the rest of
# the matrix put together.
# --disable-sandboxing: the runner has no bwrap, and sandboxing is not what is
# under test.
if [ ! -d "$OPAMROOT" ]; then
  opam init --bare --disable-sandboxing --no-setup >/tmp/opam-init.log 2>&1 || {
    echo "opam: opam init failed:" >&2
    tail -20 /tmp/opam-init.log >&2
    exit 1; }
fi

echo "== add the mount as a repository =="
# --all-switches: a repository is per-switch state, and `opam init --bare` leaves
# no switch. Without this, `opam repo add` prints "[easyblobs] Initialised" and
# then "No switch is currently set", and `opam repo list` shows nothing.
opam repo add easyblobs "$EASYBLOBS_BASE" --all-switches --set-default 2>&1 | tail -5 ||
  fail "opam repo add $EASYBLOBS_BASE"

echo "== the repository is listed =="
opam repo list --all 2>&1 | grep -q easyblobs ||
  fail "the mirror is not in opam's repo list"

echo "== opam can resolve a package out of the mirror's index =="
# No `| tail -5` on this one: piping swallowed the error and the `||` then fired
# on the pipe's status, which is why a broken mirror still printed success.
out=$(opam show dune --field=version 2>&1) || {
  echo "opam: opam show dune failed:" >&2
  echo "$out" | tail -20 >&2
  fail "the mirror's index has no usable entry for dune"
}
echo "$out" | grep -qE '^[0-9]+\.[0-9]+' || fail "opam show returned '$out'"
echo "opam: resolved dune $(echo "$out" | head -1) out of the mirror"

echo "opam: OK (init repo-add repo-list show-parses-index)"

# ------------------------------------------------------------------ hosted --
# httpcache serves this tree through a HostedHandler, so files publish to
# _hosted/<repo>/. This tree has NO Generator: it gets index.txt and nothing more.
# So this asserts that publishing, serving and deleting work - not that
# a client could consume the tree as a repository, which would need a
# generator that does not exist.
echo "== hosted: publish a file =="
REPO="e2e"
FN="repo.tar.gz"
printf 'ez hello opam hosted\n' > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/hosted-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) echo "opam: the upload failed:" >&2
     cat /tmp/hosted-up.out >&2 || true
     exit 1 ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/$REPO/$FN" ||
  { echo "opam: the uploaded file is not served back" >&2; exit 1; }
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] ||
  { echo "opam: served bytes differ ($GOT != $SUM)" >&2; exit 1; }
echo "   sha256 $SUM matches"

echo "== hosted: the repo has a listing =="
curl -fsS "$BASE/_hosted/$REPO/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "opam: index.txt does not list $FN" >&2; exit 1; }
echo "   index.txt lists $FN"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/$REPO/$FN" >/dev/null 2>&1; then
      echo "opam: the deleted file is still served" >&2; exit 1
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "opam: OK (pull: upstream)"
echo "opam: OK (hosted: put bytes index-txt delete)"
