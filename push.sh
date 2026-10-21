#!/bin/sh
# push.sh - push to GitHub using the gh token directly, because this sandbox
# has no `gh` binary and the credential helper therefore fails.
set -eu

TOKEN=$(sed -n 's/^[[:space:]]*oauth_token:[[:space:]]*//p' "$HOME/.config/gh/hosts.yml" | head -1)
if [ -z "$TOKEN" ]; then
  echo "push.sh: no oauth_token found" >&2
  exit 1
fi

REMOTE="https://x-access-token:${TOKEN}@github.com/easydev-org/easyblobs.git"
exec git -c credential.helper= push "$REMOTE" HEAD:main
