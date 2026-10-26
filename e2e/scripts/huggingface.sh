#!/bin/sh
# HuggingFace Hub: point HF_ENDPOINT at the artifact mount. huggingface_hub
# appends /{repo}/resolve/... and /api/... to the endpoint.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HF_ENDPOINT="$EASYBLOBS_BASE"
# `pip`, NOT `python3 -m pip`. easysetup's nix packaging makes a Python library
# its own derivation, so it never writes into an existing interpreter's
# site-packages: `python3 -m pip` is "No module named pip" even though `pip`
# itself works. Adding a package to an interpreter would mean building a
# python.withPackages - a different thing from installing alongside it.
pip install --no-cache-dir --quiet huggingface_hub ||
  pip install --no-cache-dir huggingface_hub
# Run assertion: fetch the file and parse it as JSON.
# The interpreter `pip` belongs to, which is the one that now has the package.
PYBIN=$(pip --version | sed -n 's/.*(python \([0-9.]*\)).*/\1/p')
PYEXE="python${PYBIN:-3}"
command -v "$PYEXE" >/dev/null 2>&1 || PYEXE=python3
"$PYEXE" - <<'PY'
import json, huggingface_hub as h
p = h.hf_hub_download('bert-base-uncased', 'config.json')
cfg = json.load(open(p))
print("huggingface: downloaded", cfg.get("model_type"), "config")
PY
