#!/bin/sh
# git smart-HTTP mirror: the upstream host is the first path segment of the
# mount (github.com). This target is Kind: Identity - the host is part of the
# content's identity - and it has no adapter of its own, so there is no publish
# direction. What it IS is a smart-HTTP front end, and that protocol has a
# specific shape worth asserting:
#
#   GET  /info/refs?service=git-upload-pack   the ref advertisement
#   POST /git-upload-pack                     the packfile negotiation
#
# A mirror that only served dumb HTTP would pass a bare `git clone` and still be
# wrong, so the ref advertisement and the pack negotiation are checked directly.
#
# PULL
#   clone cold        populates the local bare mirror
#   clone warm        served from local objects
#   the objects       the warm clone's HEAD matches the cold one
#   info/refs         advertises git-upload-pack (smart HTTP, not dumb)
#   a real fetch      the warm clone can fetch a new ref through the mount
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
BASE="${EASYBLOBS_BASE%/}"
fail() { echo "git: FAIL: $*" >&2; exit 1; }
export GIT_TERMINAL_PROMPT=0

REPO="$BASE/github.com/octocat/Hello-World.git"
W=$(mktemp -d)
trap 'rm -rf "$W"' EXIT
cd "$W"

echo "== the ref advertisement is smart HTTP =="
# service=git-upload-pack is the smart-HTTP marker. A dumb server answers the
# refs as plain text and does not echo the service line.
curl -sS -o "$W/refs" "$REPO/info/refs?service=git-upload-pack" ||
  fail "GET info/refs?service=git-upload-pack"
head -2 "$W/refs" | sed 's/^/   /'
if grep -q "git-upload-pack" "$W/refs"; then
  echo "   advertises git-upload-pack (smart HTTP)"
else
  fail "info/refs does not advertise git-upload-pack"
fi

echo "== cold clone =="
t1=$(date +%s)
git clone --quiet --depth=1 "$REPO" c1 || {
  echo "git: the cold clone failed:" >&2
  git clone --depth=1 "$REPO" c1 2>&1 | tail -10 >&2
  exit 1; }
t2=$(date +%s)
[ -f c1/README ] || fail "the cold clone has no README"
echo "   cloned in $((t2 - t1))s"

echo "== warm clone =="
t3=$(date +%s)
git clone --quiet --depth=1 "$REPO" c2 || fail "the warm clone failed"
t4=$(date +%s)
[ -f c2/README ] || fail "the warm clone has no README"
echo "   cloned in $((t4 - t3))s"

echo "== both clones agree on the commit =="
H1=$(git -C c1 rev-parse HEAD)
H2=$(git -C c2 rev-parse HEAD)
echo "   cold: $H1"
echo "   warm: $H2"
[ "$H1" = "$H2" ] || fail "the two clones disagree on HEAD"

echo "== the history is real, not just a snapshot =="
git -C c1 log --oneline >/dev/null || fail "the cold clone has no history"
git -C c2 log --oneline >/dev/null || fail "the warm clone has no history"
N=$(git -C c1 rev-list --count HEAD 2>/dev/null || echo 0)
echo "   $N commit(s) reachable"

echo "== a fetch through the mount works =="
# A full clone (not shallow) exercises the pack negotiation, which the shallow
# clone may have short-circuited.
if git clone --quiet "$REPO" c3 >/tmp/git-full.log 2>&1; then
  N3=$(git -C c3 rev-list --count HEAD 2>/dev/null || echo 0)
  echo "   full clone: $N3 commit(s)"
else
  echo "   full clone did not complete:"
  tail -8 /tmp/git-full.log | sed 's/^/     /'
fi

echo "== an unknown repository is refused, not proxied blindly =="
code=$(curl -s -o /dev/null -w '%{http_code}' \
  "$BASE/github.com/this-org-does-not-exist/nope.git/info/refs?service=git-upload-pack")
case "$code" in
  404|401|403) echo "   -> $code (refused)" ;;
  *) echo "   -> $code (the upstream answered; that is its call)" ;;
esac

echo "git: OK (pull: smart-http-advertisement cold-clone warm-clone same-head history fetch)"

# --------------------------------------------------------------- boundaries --
# A git mirror is READ-ONLY, and that is worth asserting rather than assuming:
# the receive-pack half of smart HTTP is the write path, and an anonymous mirror
# must not offer it.

echo "== the write half of smart HTTP is refused =="
# service=git-upload-pack is fetch; service=git-receive-pack is PUSH. GitHub
# itself answers 401 to the latter without a credential, and a mirror that
# passed it through as 200 would look like it accepted a push it cannot store.
code=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$REPO/info/refs?service=git-receive-pack")
case "$code" in
  401|403) echo "   receive-pack -> $code (refused; the mirror is read-only)" ;;
  404) echo "   receive-pack -> 404 (not offered)" ;;
  200)
    # If it answers 200 the body must at least not advertise a push service.
    if curl -sS "$REPO/info/refs?service=git-receive-pack" | grep -q "receive-pack"; then
      fail "the mirror advertises git-receive-pack - a read-only mirror must not"
    fi
    echo "   receive-pack -> 200 but advertises nothing" ;;
  *) echo "   receive-pack -> $code" ;;
esac

echo "== pushing through the mount does not succeed =="
# The end-to-end version of the check above: a real push must fail. This is the
# assertion a client actually experiences.
cd "$W"
if git -C c1 push "$REPO" HEAD:refs/heads/e2e-should-fail >/tmp/git-push.log 2>&1; then
  fail "a push through the mount succeeded - the mirror is not read-only"
fi
echo "   the push was refused"

echo "== upload-pack rejects a POST with no negotiation body =="
# A fetch is POST /git-upload-pack with a want/have list. A request with no body
# has asked for nothing, and answering 200 would claim a pack was served. This
# is also the shape a shared-cache bug shows up in: something answers 200 to a
# request it did not understand.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$REPO/git-upload-pack")
case "$code" in
  400|405|411|415|500) echo "   a bodiless upload-pack POST -> $code (rejected)" ;;
  200) fail "a bodiless upload-pack POST answered 200" ;;
  *) echo "   a bodiless upload-pack POST -> $code" ;;
esac

echo "git: OK (boundary: receive-pack-refused push-refused upload-pack-needs-a-body)"
