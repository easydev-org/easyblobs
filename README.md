# easyblobs

A multi-protocol package registry: it mirrors public ecosystems (npm, PyPI,
Maven, OCI, ...) through a cache, serves operator-published ("hosted") packages,
and can transparently fetch arbitrary HTTP(S) assets. In the platform
architecture it replaces Forgejo's container registry + package browsing.

## Stateless deployment

By default easyblobs keeps everything under `--data` (sqlite + a filesystem CAS).
To run it **stateless** — no persistent volume for the metadata or the blobs —
point the two storage seams at managed services:

### Metadata → Postgres

```sh
EASYBLOBS_DB_DRIVER=postgres \
EASYBLOBS_DB_DSN='host=pg user=easyblobs password=… dbname=easyblobs sslmode=require' \
  ./easyblobs --listen :8080 --data ./data
```

- Drivers: `sqlite` (default) | `postgres` | `mysql` (mariadb is aliased to
  mysql). `EASYBLOBS_DB_DSN` is the DSN for the chosen driver.
- The names are **shared with the easyvcs engine**: `EASYVCS_DB_DRIVER` /
  `EASYVCS_DB_DSN` are honored when the `EASYBLOBS_*` ones are unset, so one
  deployment configures both.
- **Unset ⇒ unchanged**: with no driver set, metadata is sqlite at
  `<data>/pkglab.db` — exactly the historical behavior.

### Blobs → S3

```sh
EASYBLOBS_S3_ENDPOINT=http://garage.worker.svc.cluster.local:3900 \
EASYBLOBS_S3_BUCKET=easyblobs \
EASYBLOBS_S3_REGION=us-east-1 \
EASYBLOBS_S3_ACCESS_KEY=… EASYBLOBS_S3_SECRET_KEY=… \
EASYBLOBS_S3_PREFIX=easyblobs \
  ./easyblobs --listen :8080 --data ./data --blob-backend=s3
```

| Env | Meaning |
|---|---|
| `EASYBLOBS_S3_ENDPOINT` | S3-compatible endpoint (garage, MinIO, Ceph RGW) |
| `EASYBLOBS_S3_BUCKET` | bucket (required) |
| `EASYBLOBS_S3_REGION` | region (default `us-east-1`) |
| `EASYBLOBS_S3_ACCESS_KEY` / `EASYBLOBS_S3_SECRET_KEY` | static credentials |
| `EASYBLOBS_S3_PREFIX` | key prefix (default `easyblobs`) |
| `EASYBLOBS_S3_PATH_STYLE` | `0` disables path-style (default: enabled) |

Putting both together gives a fully stateless instance: metadata in Postgres,
blobs in S3, nothing on local disk.

### Fully stateless with postgres + S3

With `EASYBLOBS_DB_DRIVER=postgres` and `--blob-backend=s3` there is **nothing
that must stay on local disk**: metadata lives in Postgres, blobs (including
OCI layers and Git LFS objects) live in the S3 bucket, and in-progress OCI
uploads are staged in that same bucket.

> The git **proxy** (a local `git clone --mirror` cache per repository) has
> been removed — it was the only component that required a local filesystem.
> The **Git LFS** adapter remains (pure Go, CAS-backed): it negotiates
> `/info/lfs/objects/batch` and serves objects straight from the blob store.

**OCI upload staging follows the blob backend**: with `--blob-backend=s3` the
in-progress OCI chunked-upload bytes are staged in the same S3 bucket (under
`<prefix>/uploads/…`), so they no longer occupy `<data>/oci-uploads`; with the
default filesystem backend they stay at `<data>/oci-uploads` as before.

### Request body limit

`EASYBLOBS_MAX_BODY` (bytes) caps a single request body; the default is **8 GiB**.
Very large VM / ML image layers may need a larger value, e.g.
`EASYBLOBS_MAX_BODY=34359738368` (32 GiB).

## Authentication

Two backends, selected by `EASYBLOBS_AUTH`:

| `EASYBLOBS_AUTH` | Behaviour |
|---|---|
| `static` (**default**) | Local credential table. `--tokens=<token>=write` seeds it; an instance with no users is **open** (anonymous read/write, dev mode). Unchanged historical behavior. |
| `easyvcs` | Delegate identity to **easyvcs** (the platform's single identity authority). Every bearer/raw token is validated against `GET {easyvcs}/api/v1/whoami` (45s TTL cache) via `platform/identity`. Set `EASYBLOBS_EASYVCS_URL` to the easyvcs base. |

In `easyvcs` mode agent-gateway can pass a **user's own token** straight through
to easyblobs — there is one permission model, enforced in one place.

- **Pull** is anonymous; **push/delete** require a token easyvcs authenticates.
- **Namespace ownership** is not yet enforced per-namespace: easyvcs `/whoami`
  answers *who* a token is, not *which namespaces* it may write, so an
  authenticated user may currently write any namespace. A `NamespaceAuthorizer`
  hook (`core.RemoteAuth`) is the seam for the per-namespace check once platform
  exposes it. See `DEVELOP.md`.

```
EASYBLOBS_AUTH=easyvcs \
EASYBLOBS_EASYVCS_URL=http://easyvcs-server.worker.svc.cluster.local:8996 \
  ./easyblobs --listen :8080 --data ./data
```

> **`--tokens` takes the value verbatim.** easyblobs does **not** expand `$VAR`
> or `$(VAR)` in flag values — when you write `--tokens=$(EASYBLOBS_TOKEN)=write`
> it is **Kubernetes** (container `args`) that expands `$(EASYBLOBS_TOKEN)` before
> the process starts. Running the binary directly (docker run, systemd, a shell)
> must expand it yourself, e.g. `--tokens="$EASYBLOBS_TOKEN=write"`; otherwise the
> literal `$(EASYBLOBS_TOKEN)` is seeded and no client authenticates.

## Documentation
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — how the registry is put together
  (protocols, targets, blobs, index, request lifecycle).
- [`CLIENTS.md`](CLIENTS.md) — per-protocol client setup and how hosted vs
  upstream packages are distinguished.
- [`docs/agent-gateway-integration.md`](docs/agent-gateway-integration.md) —
  the interface contract for using easyblobs as the image / package backend.
- [`DEVELOP.md`](DEVELOP.md) — engineering conventions, including the
  **file-size policy** (code files ≤500 lines, never over 1000).
- **`easy-vcs/deploy:PUBLISHING.md`** — platform policy: publish to / consume
  from easyblobs instead of GitHub or public registries (per-protocol commands).

## Build & run

```sh
cd cmd && go build          # produces ./easyblobs
./easyblobs --listen :8080 --data ./data
```

This is a `go.work` multi-module workspace; see [`DEVELOP.md`](DEVELOP.md) for
how to build and test each module.
