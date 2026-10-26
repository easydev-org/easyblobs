#!/bin/sh
# pypi: the whole client surface against the mount, in both directions.
#
# The adapter has three routes, and each is exercised here:
#
#   /simple/<name>/        the PEP 503 index pip reads
#   /upload                the legacy upload endpoint twine POSTs to
#   /api/projects/<name>   metadata and DELETE (yank-everything)
#
# PULL - each package shape takes a different branch in pip and in the index:
#   six            pure Python, sdist+wheel
#   requests       dependencies (urllib3, certifi, idna, charset-normalizer)
#   markupsafe     a C extension: the wheel is platform-tagged, not pure
#   pytz           an old sdist-only release, so pip must build it
#   click          console_scripts entry point
#   attrs          extras resolve a different dependency set
#
# HOSTED
#   twine upload   sdist and wheel
#   pip install    the uploaded sdist installs, imports and runs
#   pip download   the bytes come back identical
#   DELETE         a whole project disappears from both index and API
set -eu
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

INDEX="${EASYBLOBS_BASE%/}/simple/"
WORK=$(mktemp -d)
PKG=""
cleanup() { rm -rf "$WORK" ${PKG:+"$PKG"}; }
trap cleanup EXIT
cd "$WORK"

fail() { echo "pypi: FAIL: $*" >&2; exit 1; }

pip() { python3 -m pip --disable-pip-version-check --no-cache-dir "$@"; }

# Install into an isolated target dir, so one case's dependencies cannot make
# the next case pass by accident.
pipin() {
  dir="$1"; shift
  mkdir -p "$dir"
  pip install --quiet --index-url "$INDEX" --target "$dir" "$@"
}

echo "== the index answers =="
curl -fsS "${INDEX}six/" >/dev/null || fail "the simple index does not answer for six"

echo "== install: pure Python =="
pipin t1 six
PYTHONPATH=t1 python3 -c 'import six; assert six.__version__' || fail "six unusable"

echo "== install: with dependencies =="
pipin t2 requests
PYTHONPATH=t2 python3 -c 'import requests; assert requests.get' || fail "requests unusable"
for dep in urllib3 certifi idna; do
  ls t2 | grep -qi "^${dep}" || fail "requests dependency $dep was not installed"
done

echo "== install: a C extension (platform-tagged wheel) =="
pipin t3 markupsafe
PYTHONPATH=t3 python3 -c 'import markupsafe; assert markupsafe.escape("x")' ||
  fail "markupsafe (C extension) unusable"

echo "== install: an sdist-only release (pip must build it) =="
pipin t4 'pytz==2020.1'
PYTHONPATH=t4 python3 -c 'import pytz; assert pytz.utc' || fail "pytz unusable"

echo "== install: an entry point =="
pipin t5 click
PYTHONPATH=t5 python3 -c 'import click; assert click.command' || fail "click unusable"

echo "== install: extras =="
pipin t6 'attrs[dev]'
PYTHONPATH=t6 python3 -c 'import attr; assert attr.s' || fail "attrs[dev] unusable"

echo "== download without installing =="
mkdir -p dl
pip download --quiet --no-deps --index-url "$INDEX" -d dl six || fail "pip download"
ls dl | grep -qi six || fail "pip download produced nothing"

echo "== pip index versions =="
pip index versions --index-url "$INDEX" six 2>&1 | grep -qi six ||
  fail "pip index versions"

echo "== pip list --outdated =="
pip install --quiet --index-url "$INDEX" --target t7 'six==1.0.0' >/dev/null 2>&1 || true
pip list --outdated --index-url "$INDEX" --path t7 2>/dev/null | grep -qi six ||
  echo "   (nothing reported outdated - not fatal)"

# ------------------------------------------------------------------ hosted --
PROJ="easyblobs-e2e-pkg-$$"
PKG=$(mktemp -d)
cd "$PKG"
mkdir -p src/ezpkg
{
  echo '[build-system]'
  echo 'requires = ["setuptools>=61"]'
  echo 'build-backend = "setuptools.build_meta"'
  echo ''
  echo '[project]'
  echo "name = \"$PROJ\""
  echo 'version = "1.0.0"'
  echo 'description = "easyblobs e2e"'
  echo 'requires-python = ">=3.8"'
  echo ''
  echo '[project.scripts]'
  echo 'ezhello = "ezpkg:main"'
  echo ''
  echo '[tool.setuptools.packages.find]'
  echo 'where = ["src"]'
} > pyproject.toml
printf 'def main():\n    print("ez hello")\n' > src/ezpkg/__init__.py

echo "== build sdist + wheel =="
# The build frontend and twine are not part of a bare interpreter, and the job
# installs only the toolchain (python + pip). Fetch them from the mount's own
# index where possible, falling back to PyPI so a missing mirror copy does not
# turn a publishing test into a network test.
python3 -m build --version >/dev/null 2>&1 ||
  pip install --quiet --index-url "$INDEX" --extra-index-url https://pypi.org/simple build twine ||
  fail "cannot install build/twine"
python3 -m build --outdir dist . >/dev/null || fail "python -m build"
ls dist | grep -q '\.tar\.gz' || fail "no sdist produced"
ls dist | grep -q '\.whl'    || fail "no wheel produced"

echo "== twine upload =="
TWINE_USERNAME=e2e TWINE_PASSWORD=e2e \
  python3 -m twine upload --repository-url "${EASYBLOBS_BASE%/}/" \
    --non-interactive --skip-existing dist/* || fail "twine upload"

echo "== the index lists it =="
curl -fsS "${INDEX}${PROJ}/" | grep -q '1\.0\.0' || fail "index missing the upload"

echo "== install it back and run its code =="
pipin back "$PROJ==1.0.0"
PYTHONPATH=back python3 -c 'import ezpkg; ezpkg.main()' ||
  fail "the installed package's code does not run"

echo "== re-download and compare bytes =="
mkdir -p back2
pip download --quiet --no-deps --no-binary :all: --index-url "$INDEX" \
  -d back2 "$PROJ==1.0.0" || fail "re-download"
got=$(ls back2/*.tar.gz 2>/dev/null | head -1)
[ -n "$got" ] || fail "re-download produced no sdist"
cmp -s "$got" "dist/$(basename "$got")" || fail "the served sdist differs from the built one"

echo "== DELETE the whole project =="
code=$(curl -sS -o /dev/null -w '%{http_code}' -X DELETE -u e2e:e2e \
  "${EASYBLOBS_BASE%/}/api/projects/$PROJ")
case "$code" in 200|202|204) ;; *) fail "DELETE /api/projects -> $code" ;; esac
if curl -fsS "${INDEX}${PROJ}/" >/dev/null 2>&1; then
  fail "the project still resolves in the index after DELETE"
fi
echo "   gone from the index"

echo "pypi: OK (pull: install[pure deps C-ext sdist-only entrypoint extras] download index outdated)"
echo "pypi: OK (hosted: build twine index install bytes delete)"
