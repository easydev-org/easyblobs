#!/bin/sh
# HuggingFace Hub: point HF_ENDPOINT at the artifact mount. huggingface_hub
# appends /{repo}/resolve/... and /api/... to the endpoint.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HF_ENDPOINT="$EASYBLOBS_BASE"
# The easysetup `pip` setup carries ITS OWN interpreter (3.13) and does not put
# pip into the `python` setup's 3.14, so `python3 -m pip` is "No module named
# pip". Pick the interpreter that actually has pip rather than assuming.
PY=""
for cand in python3 python3.13 python; do
  command -v "$cand" >/dev/null 2>&1 || continue
  if "$cand" -c 'import pip' >/dev/null 2>&1; then PY="$cand"; break; fi
done
[ -n "$PY" ] || { echo "huggingface: no interpreter with pip on PATH" >&2; exit 1; }
echo "huggingface: using $PY ($($PY --version 2>&1))"
"$PY" -m pip install --no-cache-dir --quiet huggingface_hub ||
  "$PY" -m pip install --no-cache-dir huggingface_hub
# Run assertion: fetch the file and parse it as JSON.
# Same interpreter as above: it is the one that has the package.
"$PY" - <<'PY'
import json, huggingface_hub as h
p = h.hf_hub_download('bert-base-uncased', 'config.json')
cfg = json.load(open(p))
print("huggingface: downloaded", cfg.get("model_type"), "config")
PY
