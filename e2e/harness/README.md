# artifact pull-through e2e harness

Verifies each protocol adapter end-to-end with the client pointed STRAIGHT at
the artifact Service's explicit URL (`http://artifact-<p>.<ns>.svc.cluster.local
/artifacts/<proto>/...`). There is no intercepting sidecar, no MITM CA and no
DNS spoofing: artifact itself fetches the upstream (through the cluster proxy,
see `HTTP_PROXY` on the Deployment). Success = the client's package manager
installs/downloads a package from artifact AND the artifact CAS grew (proving
the bytes were cached locally).

## Layout
- `deploy-protocol.sh` — one Deployment+Service per protocol (`artifact-<p>`);
  each mounts exactly one protocol + serves it on port 80.
- `run.sh` — matrix driver: per protocol it creates a tool pod, runs
  `scripts/<p>.sh` (which reads `EASYBLOBS_BASE`), and asserts the CAS grew.
  Protocols run concurrently (`JOBS`, default 6).
- `parallel.sh` — shared bounded worker pool: `run_pool <fn> <proto...>` runs
  `fn` for each protocol with at most `JOBS` in flight, captures each worker's
  log to its own file, then prints them in protocol order and aggregates the
  PASS/FAIL/SKIP result files. `JOBS=1` reproduces serial execution.
- `scripts/<p>.sh` — the client command per protocol, mounted at `/scripts`.
  Each script points its package manager at `$EASYBLOBS_BASE` (the protocol
  mount); `git`/`ivy`/`netcache` append the upstream host to the path.

## Usage
The suite scripts are plain shell scripts; run them through `bash` (the repo
checks them out non-executable, so `./run.sh` may not work):

```sh
NS=temp bash deploy-protocol.sh                       # all protocols
PROTOCOLS="npm pypi go cargo maven" bash run.sh       # subset matrix
JOBS=12 bash run.sh                                   # parallelism (default 6)
KEEP=1 PROTOCOLS=cargo bash run.sh                    # keep the pod for debugging
```

## Prereqs
- namespace + images: build/push `artifact` first (`../build-image.sh`); tool
  images are pulled through easy-platform's own OCI mirror.
- artifact with explicit addressing (no host-driven resolution, no egress
  derivation) — the client is responsible for pointing at the mount.

## Unified mount
`deploy-unified.sh` deploys ONE artifact Deployment+Service mounting every
protocol; the client reaches each protocol by path (`/artifacts/<proto>/...`).
`UNIFIED_SVC=artifact-unified bash run.sh` and
`UNIFIED_SVC=artifact-unified bash ../lifecycle/run.sh` prove the topology
works against one instance.

## Search/aux matrix
`run-search.sh` drives each client's SEARCH (or other auxiliary) endpoint
against the explicit URL and asserts real results. With `GOSUMDB` at its
default the go build verifies against `sum.golang.org`, which the adapter
caches and serves.

## Lifecycle harness
`../lifecycle/run.sh` walks five stages per protocol — publish (A) → public
(B installs a well-known upstream package) → private (B installs A's package
from clean caches) → upgrade (A publishes v2, B consumes it) → delete — with
the protocol's REAL client, pointed at the explicit URL. A and B are separate
processes with isolated HOMEs/caches in one pod.

- Hosted families (**debian/apk/rpm/conda**) publish into the reserved hosted
  segment `/artifacts/<format>/_hosted/<repo>/...` (self-published; never
  probed as a proxy) and install it with apt/apk/dnf/conda.
- **cargo**, **rubygems**, **pub** treat delete as yank/retract (no
  unpublish in the client).
- Pipelines that need a network bootstrap (composer twine, helm cm-push,
  abuild, rpmbuild, hex archive) install their tooling from the mirror
  first; helm's cm-push plugin is baked into the tool image.

## Notes / gotchas
- Clients whose upstreams have no configurable base URL (OCI registries other
  than Docker Hub) need a distinct Service host per registry: the docker
  client carries the registry in the Host header, never the path.
- `oci` resolves a manifest list and GETs a layer, exercising the 307→CDN
  redirect the adapter follows server-side.

## Deviations
- **nuget**: its layers come off a Southeast-Asia Azure CDN that this cluster's
  egress reaches slowly, so the ~18MB layer pull can time out. It uses the same
  HTTP pull-through path as every other protocol and passes when the CDN is
  fast.
- **swift**: SwiftPM has NO public registry upstream (`api.spm.swift.org` and
  `packages.swift.org` are NXDOMAIN as of 2026-09); its normal resolution path
  is a git clone from GitHub. The script therefore exercises the adapter's
  registry HTTP face directly (identifiers + releases) plus a local compile.
- **protobuf**: the Buf Schema Registry is a Connect/gRPC service; the `buf`
  CLI talks gRPC directly, so the script drives the adapter's HTTP `/v1` face
  and asserts the response is fetched and served.
