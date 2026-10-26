#!/bin/sh
# HuggingFace Hub: point HF_ENDPOINT at the artifact mount. huggingface_hub
# appends /{repo}/resolve/... and /api/... to the endpoint.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HF_ENDPOINT="$EASYBLOBS_BASE"
# `pip` may be a separate setup from the interpreter it was built for, so it can
# exist without belonging to the python3 on PATH. Ask the interpreter for its own
# pip, and let the error through - `--quiet ... >/dev/null 2>&1` turned a real
# failure into a silent one.
python3 -m pip install --no-cache-dir --quiet huggingface_hub ||
  python3 -m pip install --no-cache-dir huggingface_hub
# Run assertion: fetch the file and parse it as JSON.
# python3, not python: the setupless interpreter ships python3 and nothing else.
python3 - <<'PY'
import json, huggingface_hub as h
p = h.hf_hub_download('bert-base-uncased', 'config.json')
cfg = json.load(open(p))
print("huggingface: downloaded", cfg.get("model_type"), "config")
PY
