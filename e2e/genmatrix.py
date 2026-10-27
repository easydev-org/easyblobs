#!/usr/bin/env python3
"""genmatrix.py - turn e2e/ci-matrix.tsv into the two GitHub Actions matrices.

The TSV is the single source of truth (protocol <TAB> runner <TAB> why). A
runner is either:

    ubuntu-latest        the client is installed by a GitHub Action on the
                         runner, so the protocol gets its own job there.
    container:<image>    the client needs that distro's userland (the three
                         distro package managers), so the job keeps a container.

Two matrices are produced because the two job shapes differ:

    --app      {"protocol": ["npm", "pypi", ...]}     one job per protocol
    --distro   {"include": [{"protocol": .., "image": ..}, ...]}

ONLY=a,b restricts the run to those protocols (used by the workflow_dispatch
input so one failing protocol can be retried without the whole matrix).
"""

import json
import os
import sys

TSV = "e2e/ci-matrix.tsv"


def rows():
    """Yield (protocol, runner) pairs, failing loudly on a malformed row.

    A silently dropped protocol is how a suite quietly stops covering
    something, so every problem here is an error, not a skip.
    """
    only = {p.strip() for p in os.environ.get("ONLY", "").split(",") if p.strip()}
    seen = set()
    for raw in open(TSV, encoding="utf-8"):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split("\t")
        if len(parts) < 2:
            raise SystemExit(f"genmatrix: malformed row (need protocol<TAB>runner): {raw!r}")
        proto, runner = parts[0].strip(), parts[1].strip()
        # Third column: does this protocol have a HOSTED fixture? A protocol
        # makes two promises and they are tested as separate jobs, so the set
        # has to be stated in the source of truth rather than guessed.
        hosted = parts[2].strip() if len(parts) > 2 else "pull-only"
        if hosted not in ("hosted", "pull-only", "build-only"):
            raise SystemExit(
                f"genmatrix: third column must be 'hosted', 'pull-only' or "
                f"'build-only', got {hosted!r} for {proto!r}"
            )
        # Fifth column: the easysetup setups this protocol needs. It lives here
        # rather than in 25 `if:` guards in the workflow, and the workflow just
        # passes it through.
        tools = parts[4].strip() if len(parts) > 4 else ""
        if not proto or not runner:
            raise SystemExit(f"genmatrix: empty protocol or runner: {raw!r}")
        if proto in seen:
            raise SystemExit(f"genmatrix: duplicate protocol {proto!r}")
        seen.add(proto)
        if only and proto not in only:
            continue
        yield proto, runner, hosted, tools
    if only:
        unknown = only - seen
        if unknown:
            raise SystemExit(f"genmatrix: ONLY names unknown protocol(s): {sorted(unknown)}")


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "--app"
    app, distro = [], []
    hosted = []
    build_only = []
    for proto, runner, direction, tools in rows():
        if runner.startswith("container:"):
            image = runner.split(":", 1)[1].strip()
            if not image:
                raise SystemExit(f"genmatrix: container row without an image: {proto}")
            # The distro row keeps its direction. The job used to hardcode
            # E2E_DIRECTION=pull, so a container protocol that can publish
            # (debian, rpm - both have HostedHandler and generate their own
            # index) could never have a hosted run: the marker was read here and
            # then thrown away.
            # ci-run.sh takes "pull" / "hosted" / "build"; the TSV says
            # "pull-only" for the same thing. Translate once, here, so the
            # workflow can pass the value straight through.
            wire = "pull" if direction == "pull-only" else direction
            distro.append({"protocol": proto, "image": image, "direction": wire})
        elif runner == "ubuntu-latest":
            app.append({"protocol": proto, "tools": tools})
            # The hosted matrix is the subset that has a fixture to publish with.
            # A protocol in "app" without one still gets a pull job - it just has
            # no hosted job, because there is nothing to publish.
            if direction == "hosted":
                hosted.append({"protocol": proto, "tools": tools})
            elif direction == "build-only":
                # Read-only mirror: its fixture is built, nothing is published.
                build_only.append({"protocol": proto, "tools": tools})
        else:
            raise SystemExit(
                f"genmatrix: unknown runner {runner!r} for {proto!r} "
                f"(expected ubuntu-latest or container:<image>)"
            )

    if mode == "--app":
        print(json.dumps({"include": app}))
    elif mode == "--hosted":
        print(json.dumps({"include": hosted}))
    elif mode == "--build-only":
        # Read-only mirrors: the fixture is built, nothing is published.
        print(json.dumps({"include": build_only}))
    elif mode == "--distro":
        print(json.dumps({"include": distro}))
    else:
        raise SystemExit(f"usage: genmatrix.py --app|--hosted|--distro (got {mode!r})")

    print(f"genmatrix: {len(app)} pull protocol(s), {len(hosted)} hosted, "
          f"{len(distro)} distro", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
