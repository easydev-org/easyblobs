#!/bin/sh
# Jenkins update center (updates.jenkins.io): plugin index + a plugin hpi,
# served from the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
wget -qO /tmp/uc.json "$EASYBLOBS_BASE/update-center.json"
grep -q 'updateCenterVersion' /tmp/uc.json || { echo "jenkins: bad update-center"; exit 1; }
wget -qO /tmp/git.hpi "$EASYBLOBS_BASE/download/plugins/git/latest/git.hpi"
[ -s /tmp/git.hpi ] || { echo "jenkins: empty hpi"; exit 1; }
echo "jenkins: update-center + git plugin ok"
