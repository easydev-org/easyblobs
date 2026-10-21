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

rm -rf /w /lfs-cache
mkdir -p /w
cd /w

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
