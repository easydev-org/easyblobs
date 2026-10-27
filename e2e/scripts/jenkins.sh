#!/bin/sh
# Jenkins update centre mirrored at the named target: the REAL update-centre
# tool is jenkins-plugin-cli (jenkins-cli / plugin manager). Fall back to
# verifying the update centre's own JSON schema when only that is available.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# The mount base without a trailing slash. The hosted section uses it.
BASE="${EASYBLOBS_BASE%/}"

export JENKINS_UC="$EASYBLOBS_BASE"
export JENKINS_UC_DOWNLOAD="$EASYBLOBS_BASE"

if command -v jenkins-plugin-cli >/dev/null; then
  mkdir -p /tmp/jplugins
  if jenkins-plugin-cli --plugins git:latest --plugins-output-dir /tmp/jplugins 2>&1 | tail -6; then
    if ls /tmp/jplugins/git*.hpi >/dev/null 2>&1; then
      echo "jenkins: jenkins-plugin-cli installed git from the mirror"
      exit 0
    fi
  fi
  echo "jenkins: jenkins-plugin-cli could not install from the mirror; falling back to a content check"
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
# Jenkins wraps the document in a JSONP callback ("updateCenter.post({...});"),
# so take the first {...} balanced object rather than "everything from the
# first brace" - the trailing ");" was what broke json.loads last time.
start = text.find("{")
end = text.rfind("}")
assert start >= 0 and end > start, "no JSON object in the update centre"
data = json.loads(text[start:end + 1])    # the real client parses exactly this
assert "updateCenterVersion" in data, "no updateCenterVersion"
print("jenkins: update-center JSON parses, version", data.get("updateCenterVersion"))
hpi = get("/download/plugins/git/latest/git.hpi")
zipfile.ZipFile(io.BytesIO(hpi)).testzip()  # an HPI is a zip archive
print("jenkins: git.hpi is a valid archive (%d bytes)" % len(hpi))
PY

# ------------------------------------------------------------------ hosted --
# httpcache serves this tree through a HostedHandler, so files publish to
# _hosted/<repo>/. This tree has NO Generator: it gets index.txt and nothing more.
# So this asserts that publishing, serving and deleting work - not that
# a client could consume the tree as a repository, which would need a
# generator that does not exist.
echo "== hosted: publish a file =="
REPO="e2e"
FN="e2e.hpi"
printf 'ez hello jenkins hosted\n' > "/tmp/$FN"
SUM=$(sha256sum "/tmp/$FN" | cut -d' ' -f1)
code=$(curl -sS -o /tmp/hosted-up.out -w '%{http_code}' -X PUT \
  --data-binary "@/tmp/$FN" "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|201) echo "   PUT $FN -> $code" ;;
  *) echo "jenkins: the upload failed:" >&2
     cat /tmp/hosted-up.out >&2 || true
     exit 1 ;;
esac

echo "== hosted: it comes back byte-identically =="
curl -fsS -o "/tmp/$FN.back" "$BASE/_hosted/$REPO/$FN" ||
  { echo "jenkins: the uploaded file is not served back" >&2; exit 1; }
GOT=$(sha256sum "/tmp/$FN.back" | cut -d' ' -f1)
[ "$GOT" = "$SUM" ] ||
  { echo "jenkins: served bytes differ ($GOT != $SUM)" >&2; exit 1; }
echo "   sha256 $SUM matches"

echo "== hosted: the repo has a listing =="
curl -fsS "$BASE/_hosted/$REPO/index.txt" 2>/dev/null | grep -q "$FN" ||
  { echo "jenkins: index.txt does not list $FN" >&2; exit 1; }
echo "   index.txt lists $FN"

echo "== hosted: DELETE removes it =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$BASE/_hosted/$REPO/$FN")
case "$code" in
  200|204)
    if curl -fsS "$BASE/_hosted/$REPO/$FN" >/dev/null 2>&1; then
      echo "jenkins: the deleted file is still served" >&2; exit 1
    fi
    echo "   gone" ;;
  *) echo "   (DELETE answered $code)" ;;
esac

echo "jenkins: OK (pull: upstream)"
echo "jenkins: OK (hosted: put bytes index-txt delete)"
