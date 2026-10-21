#!/bin/sh
# Jenkins update centre mirrored at the named target: the REAL update-centre
# tool is jenkins-plugin-cli (jenkins-cli / plugin manager). Fall back to
# verifying the update centre's own JSON schema when only that is available.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

export JENKINS_UC="$EASYBLOBS_BASE"
export JENKINS_UC_DOWNLOAD="$EASYBLOBS_BASE"

if command -v jenkins-plugin-cli >/dev/null; then
  jenkins-plugin-cli --plugins git:latest --plugins-output-dir /tmp/jplugins
  ls /tmp/jplugins/git*.hpi >/dev/null 2>&1 || {
    echo "jenkins: the cli did not produce a plugin"; exit 1; }
  echo "jenkins: jenkins-plugin-cli installed git from the mirror"
  exit 0
fi

# Fallback: the real client is unavailable in this image; still verify that
# BOTH artefacts the client consumes are valid (JSON parses, HPI is a zip).
python3 - "$JENKINS_UC" <<'PY'
import json, sys, urllib.request, zipfile, io, urllib.parse
base = sys.argv[1].rstrip("/")
def get(path):
    with urllib.request.urlopen(base + path, timeout=120) as r:
        return r.read()
uc = get("/update-center.json")
text = uc.decode("utf-8", "replace")
start = text.find("{")
data = json.loads(text[start:])           # the real client parses exactly this
assert "updateCenterVersion" in data, "no updateCenterVersion"
print("jenkins: update-center JSON parses, version", data.get("updateCenterVersion"))
hpi = get("/download/plugins/git/latest/git.hpi")
zipfile.ZipFile(io.BytesIO(hpi)).testzip()  # an HPI is a zip archive
print("jenkins: git.hpi is a valid archive (%d bytes)" % len(hpi))
PY
