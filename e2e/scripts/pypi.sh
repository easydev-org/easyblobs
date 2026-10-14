#!/bin/sh
# PyPI: point pip's index at the artifact /simple/ endpoint.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
pip install --no-cache-dir --disable-pip-version-check --index-url "$EASYBLOBS_BASE/simple/" six
# Import/run assertion: execute code from the installed package.
python3 -c 'import six; assert six.__version__; print("six", six.__version__)'
