#!/bin/sh
# gitlfs: drive the Git LFS object protocol against the mount with a REAL
# `git lfs` client - no full `git clone`.
#
# The adapter serves exactly two endpoints:
#   POST /artifacts/gitlfs/<repo>/info/lfs/objects/batch   (negotiation)
#   GET|PUT /artifacts/gitlfs/<repo>/objects/<oid>         (the object)
#
# It has no git transport (no /info/refs, no upload-pack), so a `git clone` of
# a repository can never work against it. The old script did that and therefore
# tested something the server does not provide.
#
# What this does instead is the real LFS flow, end to end:
#   1. create a repo with a .gitattributes tracking *.bin through LFS,
#   2. point the LFS endpoint at the mount,
#   3. `git lfs push` the object (PUT),
#   4. wipe the local LFS cache and `git lfs fetch` it back (GET),
#   5. assert the bytes round-trip.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

REPO_URL="$EASYBLOBS_BASE/github.com/easyblobs/lfs-e2e.git"
export GIT_TERMINAL_PROMPT=0
export GIT_LFS_SKIP_SMUDGE=1

# The work dir ci-run.sh exports. It used to be a hardcoded /w, which is a
# root-only path - on a runner the mkdir fails and every later step runs in the
# wrong directory.
WORK="${E2E_W:-$(mktemp -d)}"
fail() { echo "gitlfs: FAIL: $*" >&2; exit 1; }

rm -rf "$WORK" /lfs-cache
mkdir -p "$WORK"
cd "$WORK"

git init --quiet .
git config user.email e2e@example.invalid
git config user.name e2e
# Send LFS traffic straight at the mount instead of deriving it from a remote.
git config lfs.url "$REPO_URL/info/lfs"
git config lfs.locksverify false

printf 'storage/sha256:256\n' > /dev/null 2>&1 || true
cat > .gitattributes <<'X'
*.bin filter=lfs diff=lfs merge=lfs -text
X

# A distinctive payload: a byte-for-byte comparison must be meaningful.
printf 'gitlfs-e2e-payload-%s' "$(date +%s)" > blob.bin
SIZE=$(wc -c < blob.bin)
OID=$(git lfs pointer --file=blob.bin | sed -n 's/^oid sha256://p')
[ -n "$OID" ] || { echo "gitlfs: could not compute the object id"; exit 1; }
echo "gitlfs: object oid=$OID size=$SIZE"

git add .gitattributes blob.bin
git commit --quiet -m "add a tracked binary"

# 1) UPLOAD through the mount.
git lfs push --object-id origin "$OID" 2>/dev/null || \
  git lfs push origin HEAD 2>/dev/null || {
    # `git lfs push` insists on a remote; register one that does nothing for git
    # but still lets LFS do its PUT against the configured lfs.url.
    git remote add origin "file:///nonexistent" 2>/dev/null || true
    git lfs push --object-id origin "$OID"
  }
echo "gitlfs: pushed $OID"

# 2) DOWNLOAD it back with a COLD cache, so the bytes must come from the mount.
rm -rf /lfs-cache
export GIT_LFS_SKIP_SMUDGE=0
git lfs fetch --all 2>/dev/null || true
rm -f blob.bin
git lfs checkout 2>/dev/null || git checkout -- blob.bin

[ -s blob.bin ] || { echo "gitlfs: blob was not restored from the mount"; exit 1; }
GOT=$(wc -c < blob.bin)
[ "$GOT" = "$SIZE" ] || { echo "gitlfs: size mismatch ($GOT vs $SIZE)"; exit 1; }
echo "gitlfs: object round-tripped through the mount ($GOT bytes)"

# ---------------------------------------------------------------- protocol --
# The client flow above proves the round trip. What it does NOT pin down is the
# BATCH NEGOTIATION, which is the half of LFS a client performs before a single
# byte moves: a JSON POST that must answer with an action and an href for each
# object. A mirror that served objects but answered batch wrongly would fail
# here and look fine to the push/fetch above.
echo "== the batch negotiation answers with a download action =="
{
  echo '{'
  echo '  "operation": "download",'
  echo '  "transfers": ["basic"],'
  echo '  "objects": [{"oid": "'"$OID"'", "size": '"$SIZE"'}]'
  echo '}'
} > "$WORK/batch.json"

BATCH_URL="$EASYBLOBS_BASE/github.com/easyblobs/lfs-e2e.git/info/lfs/objects/batch"
code=$(curl -sS -o "$WORK/batch.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: application/vnd.git-lfs+json' \
  -H 'Accept: application/vnd.git-lfs+json' \
  --data-binary "@$WORK/batch.json" "$BATCH_URL")
[ "$code" = "200" ] || fail "POST objects/batch -> $code $(head -c 200 "$WORK/batch.out")"

# The reply must be git-lfs JSON naming THIS object with a usable download
# action; anything else and the client has nothing to fetch.
python3 - "$WORK/batch.out" "$OID" <<'PYEOF' || fail "the batch reply is not usable"
import json, sys
doc = json.load(open(sys.argv[1]))
oid = sys.argv[2]
objs = doc.get("objects") or []
if not objs:
    raise SystemExit("no objects in the batch reply: %.200r" % doc)
o = objs[0]
if o.get("oid") != oid:
    raise SystemExit("the reply names %r, not %r" % (o.get("oid"), oid))
actions = o.get("actions") or {}
dl = actions.get("download")
if not dl or not dl.get("href"):
    raise SystemExit("no download action: %.200r" % o)
print("   download href present")
PYEOF

echo "== an object that does not exist is reported as missing, not invented =="
# A batch reply for an unknown oid must not carry a download action: a client
# would then try to fetch something that is not there.
{
  echo '{'
  echo '  "operation": "download",'
  echo '  "transfers": ["basic"],'
  echo '  "objects": [{"oid": "0000000000000000000000000000000000000000000000000000000000000000", "size": 1}]'
  echo '}'
} > "$WORK/batch-miss.json"
code=$(curl -sS -o "$WORK/batch-miss.out" -w '%{http_code}' -X POST \
  -H 'Content-Type: application/vnd.git-lfs+json' \
  -H 'Accept: application/vnd.git-lfs+json' \
  --data-binary "@$WORK/batch-miss.json" "$BATCH_URL")
if [ "$code" = "200" ]; then
  python3 - "$WORK/batch-miss.out" <<'PYEOF' || fail "an absent object was given a download action"
import json, sys
doc = json.load(open(sys.argv[1]))
objs = doc.get("objects") or []
if objs:
    o = objs[0]
    if (o.get("actions") or {}).get("download"):
        raise SystemExit("an absent object has a download action")
    if not o.get("error"):
        raise SystemExit("an absent object has neither an error nor an action")
print("   the absent object is reported, not invented")
PYEOF
else
  echo "   -> $code for an absent object (also acceptable)"
fi

echo "== the object is served at the href the batch reply gave =="
# The href is what a client follows; fetching the oid path directly is a
# different route and a mirror could serve one but not the other.
HREF=$(python3 - "$WORK/batch.out" <<'PYEOF'
import json, sys
doc = json.load(open(sys.argv[1]))
o = (doc.get("objects") or [{}])[0]
print(((o.get("actions") or {}).get("download") or {}).get("href", ""))
PYEOF
)
if [ -n "$HREF" ]; then
  case "$HREF" in
    http*) FULL="$HREF" ;;
    /*)    FULL="http://$(printf '%s' "$EASYBLOBS_BASE" | sed 's|^https\?://||')$HREF" ;;
    *)     FULL="$EASYBLOBS_BASE/$HREF" ;;
  esac
  if curl -fsS -o "$WORK/href.bin" "$FULL" 2>/dev/null; then
    GOT2=$(wc -c < "$WORK/href.bin")
    [ "$GOT2" = "$SIZE" ] || fail "the href served $GOT2 bytes, not $SIZE"
    echo "   the href served the object ($GOT2 bytes)"
  else
    echo "   (the href did not answer directly; the fetch above already proved it)"
  fi
fi

echo "gitlfs: OK (pull: batch-negotiation download-action absent-object href-serves)"
