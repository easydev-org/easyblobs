#!/usr/bin/env python3
"""genmatrix.py — turn e2e/ci-matrix.tsv into a GitHub Actions matrix.

Reads the TSV (protocol <TAB> driver <TAB> why) and groups the protocols by
driver, so one CI job pulls one container image and runs every protocol that
image can drive.

Prints {"include": [{"driver": ..., "protocols": "a b c"}, ...]} on stdout
(a GitHub matrix body) and a one-line summary on stderr.

ONLY=a,b restricts the run to those protocols (used by the workflow_dispatch
input so a single failing protocol can be retried without the whole matrix).
"""

import collections
import json
import os
import sys


def main() -> int:
    only = {p.strip() for p in os.environ.get("ONLY", "").split(",") if p.strip()}
    by_driver: dict[str, list[str]] = collections.OrderedDict()
    skipped: list[str] = []

    for raw in open("e2e/ci-matrix.tsv", encoding="utf-8"):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split("\t")
        if len(parts) < 2:
            # A malformed row must be loud: silently dropping a protocol is how
            # a suite quietly stops covering something.
            print(f"genmatrix: malformed row (need protocol<TAB>driver): {raw!r}", file=sys.stderr)
            return 2
        proto, driver = parts[0].strip(), parts[1].strip()
        if not proto or not driver:
            print(f"genmatrix: empty protocol or driver in row: {raw!r}", file=sys.stderr)
            return 2
        if only and proto not in only:
            skipped.append(proto)
            continue
        by_driver.setdefault(driver, []).append(proto)

    if only:
        unknown = only - {p for ps in by_driver.values() for p in ps}
        if unknown:
            print(f"genmatrix: ONLY names unknown protocol(s): {sorted(unknown)}", file=sys.stderr)
            return 2

    include = [{"driver": d, "protocols": " ".join(ps)} for d, ps in by_driver.items()]
    print(json.dumps({"include": include}))
    total = sum(len(ps) for ps in by_driver.values())
    print(
        f"genmatrix: {total} protocol(s) across {len(include)} driver(s)"
        + (f"; skipped {len(skipped)}" if skipped else ""),
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
