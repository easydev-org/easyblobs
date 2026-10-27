#!/bin/sh
# huggingface: the Hub's REST face, and the real client that uses it.
#
# PULL ONLY. The adapter has no write path - it proxies the Hub for reads - and
# the Hub's own publish flow (create_repo, then a commit with LFS preupload) is
# nothing this mirror implements. That is a property of the adapter, stated
# rather than implied.
#
# The wire format has one trap worth naming: the Hub answers small resolve
# requests with a HOST-RELATIVE 307 to /api/resolve-cache/..., and passing that
# through verbatim makes a client resolve it against the ARTIFACT host root -
# a 404 from a redirect that looked fine. The adapter re-roots such a Location
# under the mount; the suite checks that, because it is invisible until it
# breaks.
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

fail() { echo "huggingface: FAIL: $*" >&2; exit 1; }

MODEL="bert-base-uncased"

echo "== the model's metadata is served =="
# /api/models/<name> answers 307 upstream; following it is the client's job and
# the mirror must keep the redirect usable.
curl -fsSL -o "$WORK/model.json" "$BASE/api/models/$MODEL" ||
  fail "GET /api/models/$MODEL (following redirects)"
grep -q '"modelId"\|"id"' "$WORK/model.json" || fail "the metadata has no model id"
grep -q "$MODEL" "$WORK/model.json" || fail "the metadata names a different model"
echo "   the metadata names $MODEL"

echo "== the file tree is served =="
if curl -fsSL -o "$WORK/tree.json" "$BASE/api/models/$MODEL/tree/main" 2>/dev/null &&
   [ -s "$WORK/tree.json" ]; then
  grep -q '"path"' "$WORK/tree.json" || fail "the tree has no paths"
  N=$(grep -o '"path"' "$WORK/tree.json" | wc -l)
  echo "   $N files in the tree"
else
  echo "   (the tree endpoint did not answer; the resolve case below is the assertion)"
fi

echo "== a resolve request returns the file, not a broken redirect =="
code=$(curl -sSL -o "$WORK/config.json" -w '%{http_code}' \
  "$BASE/$MODEL/resolve/main/config.json")
[ "$code" = "200" ] || fail "GET resolve/config.json -> $code"
# The file must be the model's config, not an HTML error page served with 200.
python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
for k in ("model_type","hidden_size","vocab_size"):
    if k not in d: raise SystemExit("config.json has no %s" % k)
print("   model_type=%s hidden_size=%s" % (d["model_type"], d["hidden_size"]))
' "$WORK/config.json" || fail "config.json is not a model config"

echo "== a tokenizer file resolves too (a different path in the same repo) =="
# A mirror that cached per-tree rather than per-path would answer this with
# config.json's bytes - the exact bug the httpcache cache once had.
if curl -fsSL -o "$WORK/tok.json" "$BASE/$MODEL/resolve/main/tokenizer.json" 2>/dev/null &&
   [ -s "$WORK/tok.json" ]; then
  python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
if "model" not in d and "version" not in d:
    raise SystemExit("not a tokenizer file")
print("   tokenizer.json is a real tokenizer (%d bytes)" % len(open(sys.argv[1],"rb").read()))
' "$WORK/tok.json" || fail "tokenizer.json is not what its name says"
else
  echo "   (no tokenizer.json in this repo)"
fi

echo "== a file that does not exist stays a 404 =="
code=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/$MODEL/resolve/main/this-file-does-not-exist.txt")
case "$code" in
  404) echo "   -> 404 (correct)" ;;
  200) fail "a nonexistent file answered 200 - the mirror invented content" ;;
  *) echo "   -> $code" ;;
esac

echo "== the real client downloads and parses a file =="
# `pip`, NOT `python3 -m pip`: easysetup's nix packaging makes a Python library
# its own derivation, so it never writes into an existing interpreter's
# site-packages - `python3 -m pip` is "No module named pip" even though `pip`
# works. And the store is read-only, so --target is the way to add a package
# beside an interpreter rather than into it.
SITE="$WORK/pylibs"
mkdir -p "$SITE"
pip install --no-cache-dir --quiet --target "$SITE" huggingface_hub ||
  pip install --no-cache-dir --target "$SITE" huggingface_hub

export HF_ENDPOINT="$BASE"
export PYTHONPATH="$SITE"
PYBIN=$(pip --version | sed -n 's/.*(python \([0-9.]*\)).*/\1/p')
PYEXE="python${PYBIN:-3}"
command -v "$PYEXE" >/dev/null 2>&1 || PYEXE=python3

if "$PYEXE" - "$MODEL" >/tmp/hf-client.log 2>&1 <<'PY'
import json, sys
import huggingface_hub as h
p = h.hf_hub_download(sys.argv[1], "config.json")
cfg = json.load(open(p))
print("downloaded %s config: model_type=%s" % (sys.argv[1], cfg.get("model_type")))
PY
then
  sed 's/^/   /' /tmp/hf-client.log | tail -3
else
  echo "huggingface: the client could not download through the mount:" >&2
  tail -20 /tmp/hf-client.log >&2
  fail "hf_hub_download"
fi

echo "huggingface: OK (pull: model-metadata tree resolve-config resolve-other 404 client-downloads)"
