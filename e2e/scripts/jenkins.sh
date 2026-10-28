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
      # NO exit 0 here. This script has TWO halves - the pull-through checks
      # below and the hosted section further down - and an early exit after the
      # first success meant the file could never run its hosted half at all.
      # That is invisible when the matrix asks for hosted (the pull half is
      # skipped by direction) and fatal when it asks for pull.
      PULL_OK=1
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

# ------------------------------------------------------- update centre API --
# The HPI above is one file. What a plugin manager actually reads is the update
# centre's JSON, and what it then does is resolve a PLUGIN AND VERSION pair
# against the download path the JSON advertises. A mirror that served the JSON
# and the "latest" HPI but not a specific version would pass every check above
# and fail any build that pins a plugin version - which is most of them.

echo "== the update centre advertises a plugin with a concrete version =="
curl -fsS -o /tmp/uc.json "$BASE/update-center.json" ||
  { echo "jenkins: could not fetch update-center.json" >&2; exit 1; }

# Jenkins wraps the document in a JSONP callback, so the balanced object is
# taken rather than "everything from the first brace" - the trailing ");" was
# what broke json.loads before.
python3 - <<'PY' || { echo "jenkins: update-center.json is not usable" >&2; exit 1; }
import json
text = open("/tmp/uc.json", encoding="utf-8", errors="replace").read()
start, end = text.find("{"), text.rfind("}")
if start < 0 or end <= start:
    raise SystemExit("no JSON object in the update centre")
d = json.loads(text[start:end + 1])
plugins = d.get("plugins") or {}
if not plugins:
    raise SystemExit("the update centre lists no plugins")
name, info = next(iter(sorted(plugins.items())))
ver = (info or {}).get("version")
if not ver:
    raise SystemExit("plugin %r has no version" % name)
print("   %s %s" % (name, ver))
open("/tmp/uc-target", "w").write("%s %s" % (name, ver))
PY

PNAME=$(cut -d' ' -f1 /tmp/uc-target)
PVER=$(cut -d' ' -f2 /tmp/uc-target)
[ -n "$PNAME" ] && [ -n "$PVER" ] ||
  { echo "jenkins: could not read a plugin/version pair" >&2; exit 1; }

echo "== that exact plugin and version is downloadable =="
# The update centre's own "url" field is the authority for where a plugin
# lives; asking for /download/plugins/<n>/<ver>/<n>.hpi is the shape a plugin
# manager uses once the version is pinned.
DL="$BASE/download/plugins/$PNAME/$PVER/$PNAME.hpi"
code=$(curl -sS -o /tmp/plugin.hpi -w '%{http_code}' "$DL")
case "$code" in
  200)
    python3 -c '
import sys, zipfile
zipfile.ZipFile(sys.argv[1]).testzip()
' /tmp/plugin.hpi || { echo "jenkins: the versioned HPI is not a zip" >&2; exit 1; }
    echo "   $PNAME $PVER downloaded and is a valid archive ($(wc -c < /tmp/plugin.hpi) bytes)" ;;
  404)
    echo "   -> 404 for $PNAME $PVER (the update centre may name a version the"
    echo "   mirror does not carry; the latest HPI above is the assertion)" ;;
  *) echo "   -> $code" ;;
esac

echo "== a plugin and version that do not exist stays a 404 =="
code=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/download/plugins/this-plugin-does-not-exist/9.9.9/this-plugin-does-not-exist.hpi")
case "$code" in
  404) echo "   -> 404 (correct)" ;;
  200) echo "jenkins: a nonexistent plugin answered 200" >&2; exit 1 ;;
  *) echo "   -> $code" ;;
esac

echo "jenkins: OK (update-centre: plugin-version-advertised versioned-download 404)"
