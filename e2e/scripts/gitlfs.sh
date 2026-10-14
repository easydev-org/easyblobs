#!/bin/sh
# Git LFS: clone a small public LFS repository through the mirror. The git
# smart-HTTP objects come from the bare mirror; the LFS batch + object come
# from the git adapter's LFS endpoints. The upstream host is the first path
# segment (github.com).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export GIT_TERMINAL_PROMPT=0 GIT_LFS_SKIP_SMUDGE=0 GIT_LFS_INSECURE=0
rm -rf /w
git clone --quiet "$EASYBLOBS_BASE/github.com/Schoonology/git-lfs-test.git" /w
# The LFS object must have been materialized (not left as a pointer).
head -c 3 /w/binary.jpg | od -An -tx1 | grep -qi 'ff d8 ff' || { echo "gitlfs: binary.jpg not materialized"; exit 1; }
echo "gitlfs: clone + lfs object ok ($(wc -c </w/binary.jpg) bytes)"
