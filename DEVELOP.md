# DEVELOP — engineering conventions

This document records repository-wide conventions for contributors. The
authoritative map of the code itself is `ARCHITECTURE.md`; per-client setup is in
`CLIENTS.md`; the agent-gateway integration contract is in
`docs/agent-gateway-integration.md`.

## File size policy

**Code files should stay under 500 lines, and must never exceed 1000 lines.**
This is an organization-wide requirement, not a per-repo preference: small,
single-purpose files are easier to review, navigate and merge.

- The limit applies to every source file in this repo (`*.go`).
- **Test files count too** (`*_test.go` are subject to the same ≤500 / ≤1000
  limits). A large test file should be split by concern alongside the code it
  tests.
- **Exceptions** are limited to:
  - **auto-generated files** (e.g. `go.sum`, generator output, `*_gen.go`), and
  - **pure data / manifest files** (e.g. test golden files, embedded manifests).
- **No change may push a file past 1000 lines.** A PR that grows a file toward
  the limit should split it instead — near 500 lines, consider splitting
  preemptively.

When splitting, prefer **multiple files in the same package** over new
packages: keep the exported contract unchanged (interface implementations,
exported symbols, wire behavior) and name the pieces by their responsibility,
for example `fetch.go`, `pull.go`, `publish.go`, `metadata.go`, `auth.go`.

## File sizes today

As of this writing, **no `.go` file exceeds 500 lines** (the largest is
`cmd/main.go` at 499). The oversized files this policy originally listed —
`core/remote.go` (1037) and the six adapter `lib.go` files (`conan` 811, `npm`
784, `rubygems` 720, `git` 672, `hex` 594, `pypi` 572) — have all been split
into same-package files by responsibility, with no behavior change.

For reference, `cmd/main.go` is 499 lines — **within the target**. Files in the
400–500 range should also be split preemptively when they are touched.

## How to check

List every Go file over 500 lines, largest first:

```sh
find . -name '*.go' -not -path './.git/*' \
  | while read f; do n=$(wc -l <"$f"); [ "$n" -gt 500 ] && printf "%5d %s\n" "$n" "$f"; done \
  | sort -rn
```

A healthy repo returns **nothing** from the command above.

Any file over 1000 lines is a violation:

```sh
find . -name '*.go' -not -path './.git/*' \
  | while read f; do n=$(wc -l <"$f"); [ "$n" -gt 1000 ] && printf "%5d %s\n" "$n" "$f"; done
```

## Multi-module builds

This repository is a **`go.work` multi-module workspace**: each protocol adapter
is its own module, plus `core`, `targets`, `cmd`, and a few others. Build and
test **per module** (a bare `go build ./...` from the root does not traverse
them):

```sh
# list the modules
grep -oE '\./[a-z0-9]+' go.work | tr -d './'

# build + test each one
for m in apk cargo cmd composer conan conda core debian generic git gitlfs go \
         helm hex httpcache huggingface ivy maven netcache nix npm nuget oci \
         protobuf pub pypi rpm rubygems swiftpm system s3blob targets; do
  (cd "$m" && go build ./... && go test ./...) || echo "FAILED: $m"
done
```

`cmd` is the server binary: `cd cmd && go build` produces `easyblobs`, run as
`artifact --listen :8080 --data ./data`.

## Shared platform code (`github.com/easy-platform/platform`)

Services must **not** import one another's modules (e.g. artifact must not import
easyvcs — its gorm/go-git tree would pollute artifact). Shared, dependency-light
pieces live in `github.com/easy-platform/platform` (e.g. `platform/identity`
for the identity client). Consume it by tag:

```
require github.com/easy-platform/platform v0.1.0
```

`platform` is **private and not on proxy.golang.org** (nor on github.com), so a
plain `GOPROXY=https://proxy.golang.org` build cannot resolve it. Resolve it one
of two ways:

- **Preferred — vendor it** (`go mod vendor`, build with `-mod=vendor`): the
  dependency is then in-tree and the image build needs no network for it.
- **Or — route it through the cluster git**: rewrite the import path to Forgejo
  and bypass the proxy, then build with `-mod=mod` so `go.sum` is refreshed:
  ```
  git config --global url."http://git.agent.svc.cluster.local/easy-vcs/".insteadOf "https://github.com/easy-platform/"
  export GOPRIVATE='github.com/easy-platform/*'   # skip GOPROXY for this path
  ```
  `GOPROXY` stays as-is for every other dependency.

**Artifact's own Go proxy works** (it serves `platform`'s `.zip`, and the
release image is built through it) — but `go.sum` must record the **proxy's**
checksum, not git's (see "Publishing & consuming via artifact" below).

## Publishing & consuming via artifact (not GitHub/public registries)

The platform's private package registry is **artifact**
(`http://artifact.<namespace>.svc.cluster.local`, default namespace `worker`).
Internal packages are published to and consumed from artifact — **not** GitHub /
npmjs / PyPI / crates.io / etc. This repo is Go, so:

- **Consume** (an internal Go module, e.g. `github.com/easy-platform/*`):
  ```sh
  GOPROXY="https://proxy.golang.org,http://artifact.worker.svc.cluster.local/artifacts/go" \
  GOSUMDB=off go get github.com/easy-platform/platform@v0.1.0
  ```
  Resolve internal modules **through artifact** (not git), so `go.sum` records
  the proxy-authoritative checksum — mixing routes causes a checksum mismatch.
- **Publish** (an internal Go module, write token required):
  ```sh
  curl -X PUT --data-binary @module.zip \
    "$ARTIFACT/artifacts/go/upload?name=<module>&version=vX.Y.Z"
  ```

**Authoritative per-protocol commands: `easy-vcs/deploy:PUBLISHING.md`.** The
per-protocol **client** setup (consume + hosted-vs-upstream) for every ecosystem
is this repo's `CLIENTS.md`; the executable examples are
`e2e/lifecycle/scripts/<proto>.sh`.
