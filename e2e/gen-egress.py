"""gen-egress.py - derive the proxy's egress allowlist from targets/builtin.go.

The workflow used to carry a hand-written list of upstream hosts, and every
omission surfaced the same confusing way:

    req GET /artifacts/cargo/index/an/yh/anyhow -> 404 (2ms)

i.e. the SERVER could not reach its own upstream, so a protocol looked broken.
Deriving the list from the built-in targets removes that whole class of
mistake - adding a target automatically lets the server reach it.

Usage:
    python3 e2e/gen-egress.py            # one line, comma separated
    python3 e2e/gen-egress.py --check    # exit 1 if the workflow is stale
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
BUILTIN = ROOT / "targets" / "builtin.go"
WORKFLOW = ROOT / ".github" / "workflows" / "e2e-clients.yml"

# Hosts the adapters reach that are NOT a Base in builtin.go: redirect targets
# and the object stores the registries hand back.
EXTRA = [
    "auth.docker.io",
    "index.docker.io",
    "production.cloudflare.docker.com",
    "registry-1.docker.io",
    "ghcr.io",
    "quay.io",
    "gcr.io",
    "objects.githubusercontent.com",
    "codeload.github.com",
    "raw.githubusercontent.com",
    "files.pythonhosted.org",
    "cdn-lfs.huggingface.co",
    "static.crates.io",
    "index.crates.io",
    "sum.golang.org",
    "storage.googleapis.com",
    "mirrors.fedoraproject.org",
    "download.fedoraproject.org",
]


def hosts():
    src = BUILTIN.read_text()
    found = set()
    for m in re.finditer(r'Base:\s*"https?://([^/"]+)', src):
        found.add(m.group(1).lower())
    for m in re.finditer(r'Hosts:\s*\[\]string\{([^}]*)\}', src):
        for h in re.findall(r'"([^"]+)"', m.group(1)):
            if h.startswith("*."):
                h = h[2:]
            found.add(h.lower())
    found.update(EXTRA)
    return sorted(found)


def main():
    want = ",".join(hosts())
    if "--check" in sys.argv:
        text = WORKFLOW.read_text()
        if want not in text:
            print("gen-egress: the workflow's ALLOWED_EGRESS_LIST is stale", file=sys.stderr)
            print("  expected: " + want, file=sys.stderr)
            return 1
        print("gen-egress: workflow is up to date")
        return 0
    print(want)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
