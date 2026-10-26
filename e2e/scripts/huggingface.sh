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
#
# And the store is READ-ONLY, so even the right pip cannot install into it:
#   ERROR: [Errno 13] Permission denied:
#     /nix/store/...-python3-3.14.8/lib/python3.14/site-packages/...
# `--target` is the way to add a package beside an interpreter rather than into
# it, which is exactly the relationship nix models here.
PYBIN=$(pip --version | sed -n 's/.*(python \([0-9.]*\)).*/\1/p')
PYEXE="python${PYBIN:-3}"
command -v "$PYEXE" >/dev/null 2>&1 || PYEXE=python3
SITE="$PWD/pylibs"
mkdir -p "$SITE"
pip install --no-cache-dir --quiet --target "$SITE" huggingface_hub ||
  pip install --no-cache-dir --target "$SITE" huggingface_hub
# Run assertion: fetch the file and parse it as JSON, from the target dir.
PYTHONPATH="$SITE" "$PYEXE" - <<'PY'
import json, huggingface_hub as h
p = h.hf_hub_download('bert-base-uncased', 'config.json')
cfg = json.load(open(p))
print("huggingface: downloaded", cfg.get("model_type"), "config")
PY
