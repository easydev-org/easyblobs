# artifact — agent-gateway integration contract

This document is the interface contract between **`agent-gateway`** and
**`artifact`** for the *image / package backend*. In the new architecture
(`easy-vcs/easyvcs@main`, `docs/agent-gateway-design.md` §1.2/§5) `artifact`
replaces **Forgejo's container registry + package browsing**.

**Boundary.** `artifact` is *only* a registry: it stores and serves OCI
manifests/blobs and other package formats. **Image build orchestration stays in
`agent-gateway`** (`internal/imagebuild`: fetch source → buildkit → push to
artifact). `artifact` never builds images.

Everything below was verified against a running server built from this branch
(`go build ./cmd`), with `--tokens=writetok=write,readtok=read`. Where a
capability is *planned* rather than shipped, it is explicitly marked
**PLANNED / not implemented**.

---

## 1. Summary table

| # | Capability | Endpoint(s) | Status | Notes |
|---|---|---|---|---|
| 1 | **OCI registry (pull + push)** | `/v2/...` (Distribution) | **Available** | `<host>/<org>/<name>:<tag>`; registry travels in the **Host** header |
| 2 | **catalog** (list image tags by owner/name) | `GET /v2/_catalog`, `GET /v2/<name>/tags/list` | **Available** | `_catalog` strips the deployment-host prefix; `?owner=`/`?owner=&name=` list an owner's images/tags (digest pseudo-tags filtered); `?n=`/`?last=` pagination still not implemented |
| 3 | **upstream mirror (`oci-import`)** | — | **Not applicable** | `artifact` intentionally has **no** mirror/import API; do it in `agent-gateway` via `imagebuild.Import` (buildctl `FROM <source>` re-push) |
| 4 | **auth & tenant namespace** | `GET /v2/`, `/token`, `/v2/_catalog` | **Partial** | Flat `read`/`write` tokens; **no per-org enforcement**; `Ownership` not wired for OCI. `org` is a **path convention** only (today) |

Legend: **Available** = shipped and tested; **Partial** = shipped with a
semantic gap or a missing convenience endpoint; **PLANNED / Deferred** = not
implemented (may be decided to stay unimplemented); **Not applicable** = by
design, handled elsewhere.

---

## 2. Capability 1 — OCI registry (pull + push)

**Status: available.** Standard Distribution API, spec-fixed at `/v2`.

### Naming

```
<registry-host>/<org>/<name>:<tag>          e.g.  artifact.temp.svc.cluster.local/agent-toolchain/toolchain-go:v1
```

The request path is `/v2/<org>/<name>/...` — **two path segments** after `/v2`
(tested with `/v2/agent-toolchain/toolchain-go/...`). `org` is the first path
segment of the image name; `name` is the rest.

**The registry host is carried in the `Host` header, never in the path.** A
docker/podman/skopeo/buildkit client resolves `<host>` → connects to it → sends
`GET /v2/<org>/<name>/manifests/<tag>` with `Host: <host>`. This is why
`<host>` must be the artifact Service name (`IMAGE_REGISTRY_HOST`).

### Push (buildkit / skopeo / crane)

The standard three steps; a client library does this for you:

```
POST /v2/<org>/<name>/blobs/uploads/            -> 202, Location: /v2/<org>/<name>/blobs/uploads/<session>
PUT  <location>?digest=sha256:<hex>             -> 201   (blob body)
PUT  /v2/<org>/<name>/manifests/<tag>           -> 201   (Content-Type: application/vnd.oci.image.manifest.v1+json)
```

buildkit pushes with:

```
--output type=image,name=<host>/<org>/<name>:<tag>,push=true
```

which is exactly what `agent-gateway`'s `imagebuild` already does — only the
destination host changes from the old `git.agent...` to the artifact Service.

### Pull

```
GET /v2/<org>/<name>/manifests/<ref>   (Accept: OCI manifest / docker manifest v2 / index)
GET /v2/<org>/<name>/blobs/<digest>
GET /v2/<org>/<name>/tags/list
```

Pull is **public** (anonymous) even when auth is enabled — see §5.

### Pull-through cache

If a requested image is **not** stored locally, the adapter fetches it from the
upstream registry for that host (Docker Hub by default; per-host overrides via
`-repo-upstreams`) and caches it. **It is cached under the name that was
requested** (the upstream registry host + repo), *not* under a `<org>/<name>`
you choose — see capability 3.

> Source: `oci/adapter.go`, `oci/manifest.go`, `oci/upload.go`, `oci/blob.go`.

---

## 3. Capability 2 — catalog

### 3a. `GET /v2/_catalog` — available

```json
{"repositories":["acme/app","docker.io/library/alpine"]}
```

- The **deployment's own host prefix is stripped**: a locally pushed image shows
  up as `acme/app`, not `<registry-host>/acme/app`. An explicit upstream host the
  client named (`docker.io/library/alpine`) is kept, since there the host is part
  of the content identity.
- **Gated** when auth is enabled: any valid credential (read or write) is
  required; anonymous → `401` (§5a). Pull (manifests/blobs) stays anonymous.
- **No pagination** (`?n=`/`?last=` are ignored; the full list is returned), and
  the plain form does not filter by owner — use `?owner=` (§3c) for that.

### 3b. `GET /v2/<name>/tags/list` — available, one caveat

```json
{"name":"acme/app","tags":["sha256:8f9f...","v1"]}
```

- **Caveat — for a pull-through (upstream) image, `tags` may contain
  `sha256:<hex>` digest entries** in addition to real tags. A consumer listing
  "tags" must **filter out entries containing `:`** (or matching
  `^sha256:`). Locally pushed tags (e.g. `v1`) are plain.

### 3c. `GET /v2/_catalog?owner=<org>` / `?owner=<org>&name=<name>` — available

The Forgejo equivalent of `ListContainerPackages(owner, name)` — "list the tags
of images owned by `<org>`":

```
GET /v2/_catalog?owner=<org>              -> {"images":[{"owner","name","tags":[...]}, ...]}  (all images under <org>)
GET /v2/_catalog?owner=<org>&name=<name>  -> {"owner":..,"name":..,"tags":[...]}              (one image's tags)
```

- `owner` is the **second** segment of `<host>/<owner>/<name>` (the first is the
  registry host). The host prefix is stripped from the output, and `sha256:`
  digest pseudo-tags are filtered — `tags` lists only real tags.
- An unknown `name` → `404`; an unknown `owner` → `{"images":[]}`.
- Same gating as §3a (any valid credential when auth is on).

### 3d. Admin-side listing (operator-only)

`GET /artifacts/system/packages` returns every package across all formats as
`PackageSummary{format,repository,version,media_type,digest,size,source,hosted,namespace,repo}`:

```json
{"packages":[{"format":"oci","repository":"easy-vcs/agent-gateway","version":"v1","media_type":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:...","size":1,"source":"push","hosted":true,"namespace":"easy-vcs","repo":"agent-gateway"}]}
```

- **`source`** — `"push"` (a client published it — self-hosted) or `"pull"`
  (cached from an upstream). **`hosted`** is the derived boolean
  (`source == "push"`); a browsing UI shows **only hosted** packages.
- **`namespace` / `repo`** — the repository key split: the first path segment is
  the owning namespace (`easy-vcs/agent-gateway` → `easy-vcs` + `agent-gateway`;
  npm `@acme/ui` → `@acme` + `ui`); a single-segment repository has an empty
  namespace. A UI joins (namespace, repo) to a repository and lists its
  packages/releases.

It requires a **write-level** credential (operator surface) — tested: anonymous
→ `401`, write token → `200`. Suitable for admin inventory, **not** for
per-tenant browsing.

> Source: `oci/adapter.go` (`catalog`, `listTags`), `system/lib.go` (`packages`).

---

## 4. Capability 3 — upstream mirror (`oci-import`)

**Status: not applicable — by design. `artifact` provides no mirror/import API.**

This is intentional, not a gap. There is **no** `POST /artifacts/system/oci-import`
(or any copy/mirror endpoint) — tested: `404`.

When `agent-gateway` needs to "copy an upstream image into this registry", it
does so **on the gateway side**: `internal/imagebuild.Import` performs a no-op
buildkit build (`FROM <source>`) and pushes the result to the artifact registry
at `<artifact-host>/<org>/<name>:<tag>`. This is the **same push path** as a
normal build (§2) — no extra artifact API is required.

**Why the pull-through cache cannot substitute for import:** the OCI
pull-through cache (§2, "Pull-through cache") stores bytes **under the name that
was requested**, keyed by the *upstream* registry host — e.g. a pull of
`docker.io/library/redis:7.4.2` caches under `docker.io/library/redis`. It does
**not** land the image under a `<org>/<name>` of your choosing, and it is a
read-through side effect, not a copy operation. To place an image under a
specific `<org>/<name>`, use `imagebuild.Import`.

> Source: `oci/client.go` (`GetManifest`/`GetBlob`/`ListTags` are the building
> blocks the gateway could reuse if it ever wanted an in-process copy, but no
> such endpoint exists). Reference: `workspace-gateway/internal/imagebuild`
> (`Import`).

---

## 5. Capability 4 — auth & tenant namespace

**Status: partial.** Flat `read`/`write` credentials; **no per-org enforcement**.

### 5a. What exists

- **Credential model** (`core/auth.go`): `Auth` interface with `TokenAuth`
  (static `token=level` table, `level ∈ {read, write}`) and `StoreAuth`
  (credentials in the metadata DB, SHA-256 hashed at rest; seeded via
  `--tokens`). When **no** users exist the instance is **open** (anonymous
  read/write, dev mode).
- **OCI auth flow** (verified): with auth enabled, `GET /v2/` returns
  `401` with
  `WWW-Authenticate: Bearer realm="<self-base>/token",service="oci-registry"`.
  `GET /token?service=oci-registry&scope=repository:<name>:pull[,push]` mints an
  OCI bearer token (validated with `-u u:writetok`; a `read` token yields a
  pull-only grant). The token can also be presented as `Bearer`.
- **Privileges** (verified): **pull is public** (anonymous `GET manifest/blob`
  → `200`); **push requires a write-level credential** (anonymous/`read` push →
  no upload session; `write` push → `201`).
- **Catalog gated** (verified): with auth enabled, `GET /v2/_catalog` (all three
  forms) requires **any valid credential** — read or write — and returns `401`
  with a `Bearer realm="<self-base>/token",service="oci-registry"` challenge when
  anonymous. **Pull (manifests/blobs) and `tags/list` remain anonymous.**
  (An open instance — no `--tokens`/users — leaves the catalog open too.)
- **Tenant plumbing exists but is unused for OCI**: `core/storeauth.go` defines
  `TenantTokenStore` (`TenantOfToken`/`TenantOfUsername`) and `StoreAuth.TenantID`,
  so a deployment *can* bridge an external user table and resolve a tenant per
  credential. **However**: `Registry.Owners` is **not set in `cmd`** (nil), and
  the **OCI adapter does not consult `Ownership`/`TenantID` at all**. So the
  catalog is gated only by "is there *a* credential", **not** filtered by owner,
  and a write token can push to **any** `<org>`.

### 5b. What "org namespace" means today

`<org>` in `<host>/<org>/<name>:<tag>` is **purely a path convention** — it is
just the first segment of the image name. There is **no server-side
enforcement** that a given credential may only read/write its own org.

### 5c. Two integration options

**(a) Shared token + path convention — available now.**
`agent-gateway` uses **one write token** to push every tenant's images; `org` is
only a naming convention. Isolation is enforced **on the gateway side** (it
already checks tenant `ownsOrg` before acting). 
- *Pros:* works today, no artifact change.
- *Cons:* artifact itself enforces nothing — **any holder of the token can
  read/write every org**, and the catalog leaks all namespaces.

**(b) Org-level enforcement — deferred (decided: not this round).**

- **Decision:** this round uses **(a) shared write token + path convention**.
  `artifact` having **no per-org enforcement** is a **known and accepted**
  current state — **not a gap**.
- **Rationale:** consistent with the Forgejo era (the old `workspace-gateway`
  also used a single shared token for every registry operation) — **not a
  regression**. The only writer is `agent-gateway` (the token is held
  in-cluster), and isolation is enforced **at the one point that has identity**.
- **If it is ever needed:** the tenant↔token mapping must come from **easyvcs
  (the identity source, `tenant ≡ user`)**; `artifact` must **not** invent its
  own tenant concept. It would then require wiring `Ownership` into `cmd` +
  authorizing OCI by `(oci, namespace=<org>)` + filtering `/v2/_catalog` by the
  caller.
- *Status:* **deferred / not implemented** — do **not** assume it works, and it
  is **not scheduled**.

> Source: `core/auth.go`, `core/storeauth.go`, `core/registry.go` (`Ownership`,
> `AuthorizePublish`, `CanRead`), `oci/adapter.go` (`authorize`, `challenge`,
> `tokenRealm`), `cmd/main.go` (auth selection).

### 5d. Enabling auth (deployment side)

`artifact`'s auth mode is decided **at startup** (`cmd/main.go`): it optionally
seeds credentials (`--tokens`), then checks whether **any** user/credential
exists (`meta.OpenInstance()`).

- **Open mode** (no users at all): `auth = nil` — **anonymous read and write are
  allowed**, and `/v2/token` is **not mounted** (`GET /v2/token` → `404`). This is
  the default for a fresh instance and is meant for development.
- **Closed mode** (any credential exists): `StoreAuth` is active — `GET /v2/`
  returns a `401` challenge, `/v2/token` exists, push needs a write-level
  credential, and `_catalog` needs any valid credential (pull / `tags/list` stay
  anonymous). See §5a.

**To enable it**, start with `--tokens=<token>=write`:

```sh
artifact --listen=:8080 --data=/data --tokens="$EASYBLOBS_TOKEN=write"
```

- Format is `token=level` with `level ∈ {read, write}` (`r`/`rl`/`w`/`rw` are
  also accepted); several may be comma- or space-separated. A malformed pair is
  **fatal** at startup: `seed tokens: malformed token pair "..." (want token=level)`.
- Seeding is **idempotent** — passing the same `--tokens` on every start is
  harmless (an existing token is skipped).
- `--self-base` is **not** required to enable auth.
- **Make sure the flag actually reaches the process.** A deployment that
  overrides the container `command`/`args` can silently drop `--tokens`, leaving
  the instance open. Inject the token from a Secret rather than inlining it.
- **The data directory must be persistent** (a PVC). Credentials live in
  `<data>/pkglab.db`; an ephemeral data dir means a fresh DB on every restart,
  and without `--tokens` the instance falls back to **open**.

**Two checks to tell which mode you are in:**

```sh
# 1) /v2/token exists ONLY in closed mode -> a 404 means OPEN.
curl -s -o /dev/null -w '%{http_code}\n' http://<artifact>/v2/token

# 2) The startup log states the mode (Go's log writes to STDERR):
#    open   -> "auth: no users registered — open instance (anonymous read/write)"
#    closed -> "auth: seeded credential (level write)"
```

> Go's `log` package writes to **stderr** by default; a deployment that captures
> only stdout will show no auth line at all.

**Consequence for `agent-gateway`:** whether its `Authorization: Bearer` is
*accepted* is separate from whether the instance is *open* — rejection depends
entirely on the deployment being in closed mode. A **production deployment must
verify it is closed** (the `/v2/token` check above).

---

## 6. Mapping from the old Forgejo interfaces

| workspace-gateway (Forgejo) | → artifact | Status |
|---|---|---|
| `ListContainerPackages(owner, name)` | `GET /v2/_catalog?owner=<org>` / `?owner=<org>&name=<name>` (host prefix stripped, `sha256:` digest rows filtered); falls back to `GET /v2/_catalog` + `GET /v2/<name>/tags/list` | **Available** |
| `oci-import` (image mirror) | **No artifact API** — `agent-gateway` `imagebuild.Import` re-pushes via buildctl (`FROM <source>`) to `<host>/<org>/<name>:<tag>` (§4) | N/A by design |
| buildkit push target | artifact OCI registry: `--output type=image,name=<host>/<org>/<name>:<tag>,push=true` (§2) | Available |
| registry host for image refs | `IMAGE_REGISTRY_HOST` = artifact Service host (§7) | Available |

`ListOCIImages` in the gateway builds each `OCIImage.Ref` as
`<registry-host>/<owner>/<name>:<tag>`. The owner/name come from
`GET /v2/_catalog?owner=<org>` (§3c), whose output already has the
deployment-host prefix stripped and digest rows filtered.

---

## 7. Deployment wiring

`agent-gateway` env (from `docs/agent-gateway-design.md` §6):

```yaml
EASYBLOBS_URL:        http://artifact.<ns>.svc.cluster.local:80   # base URL (self-URLs, /token realm)
IMAGE_REGISTRY_HOST: <artifact host>                             # replaces the old git.agent... registry host
```

**artifact's own egress** (it fetches upstreams itself — there is **no sidecar**):

- Route upstream fetches through an HTTP proxy: set `HTTP_PROXY` / `HTTPS_PROXY`
  (e.g. `http://mihomo.<ns>.svc.cluster.local:7890`) and `NO_PROXY`
  (include `.svc`, `.svc.cluster.local`). Or set `--upstream-proxy=<url>`
  (`--upstream-proxy=none` forces direct).
- Fully local / no upstream: `--air-gap` (disables all pull-through).
- Relevant flags: `--listen --data --protocols --self-base --tokens --air-gap
  --blob-backend --upstreams --repo-upstreams --upstream-proxy --gc-interval
  --gc-grace`.

`--self-base` must be the externally reachable origin (`http://artifact.<ns>.svc.cluster.local`)
so the OCI `/token` realm and emitted self-URLs are correct.

---

## 8. Known limitations & planned work

| Item | Status | Detail |
|---|---|---|
| Per-org / per-tenant enforcement for OCI | **Deferred (decided: option (a), shared token + path convention)** | Decided to stay unimplemented this round — **not a gap**: no per-org enforcement is known and accepted, matching the Forgejo era; any future tenant↔token mapping comes from easyvcs (`tenant ≡ user`), not artifact (§5c-b) |
| `_catalog` host-prefix on repo names | **Resolved** | Deployment host prefix is now stripped; explicit upstream hosts are kept (§3a) |
| `_catalog` pagination (`?n=`, `?last=`) | **Missing** | Ignored; full list returned (§3a) |
| `_catalog` owner/name filter | **Available** | `?owner=`/`?owner=&name=` shipped (§3c) |
| `_catalog` access control | **Available** | Requires any valid credential when auth is on (pull stays anonymous) (§3a, §5a) |
| `tags/list` digest pseudo-tags | **By design (caveat)** | Filter entries matching `^sha256:` (§3b) |
| `oci-import` / mirror API | **Not applicable** | Done gateway-side via buildctl re-push (§4) |

---

## 9. References

| Concept | Source |
|---|---|
| OCI routes, catalog, tags/list, authorize | `oci/adapter.go` |
| OCI manifest/blob pull-through + cache | `oci/manifest.go`, `oci/blob.go` |
| OCI upstream client (manifest/blob/tags) | `oci/client.go` |
| OCI push (upload sessions) | `oci/upload.go` |
| Credential model, levels, OCI bearer | `core/auth.go` |
| Store-backed auth, tenant plumbing | `core/storeauth.go` |
| Ownership interface | `core/registry.go` |
| Operator surface (`/packages`, `/stats`, …) | `system/lib.go` |
| Auth selection / flags | `cmd/main.go` |
| Client setup (per protocol) | `CLIENTS.md` |
| Architecture (explicit addressing, no sidecar) | `ARCHITECTURE.md` |

*This contract reflects the source and a live server at the time of writing; the
source is authoritative for the latest behavior.*

---

## 10. Publishing: what lands where

Where a producer's artifacts are stored in `artifact`. Two writers exist today;
a third (non-OCI packages) has **no publish flow yet**.

### OCI images — the one live publish path

`agent-gateway` (`internal/imagebuild`, via easyops/buildkit) pushes images to
`artifact`'s registry:

```
<artifact-host>/<org>/<name>:<tag>      ->  request path  /v2/<org>/<name>
```

- `<org>` is the easyvcs **org (namespace)**; the second segment is the
  **image name** (the caller's choice), *not* a repo name. The repo is only the
  **source** of the build (an easyvcs `/archive`).
- The registry host travels in the **Host** header (spec-fixed), never the path.
- **No per-org enforcement**: any write token may push any `<org>`; `<org>` is a
  **path convention** and isolation is enforced in the gateway (`ownsOrg`),
  not in artifact (§5c-b).

### Non-OCI packages (npm/pypi/…) — no publish flow yet

`agent-gateway` does **not** publish non-OCI packages to `artifact` today (its
`workspace.v1` has no package/publish RPC; it only *consumes* the mirrors). If
one is introduced, the landing is the protocol's **hosted** face:
`/artifacts/<proto>/_hosted/<repo>/…` (or `/-/<repo>/…`), with `<repo>` chosen
by the producer. No `easyvcs {ns}/{name} → artifact repo key` mapping exists for
these; each protocol already namespaces its own way (npm scope, Go module path,
…).

### Releases / arbitrary assets — use the generic store

There is **no release model** in easyvcs (`ListReleases` returns empty). Release
assets, build outputs, and any non-package blob are stored in `artifact`'s
**generic** store under the reserved `_hosted` segment, keyed by the owning
namespace so it lines up with the OCI org:

```
PUT/GET/DELETE /artifacts/generic/_hosted/<ns>/<name>/<tag>/<asset>
```

`<ns>` is the easyvcs org (or any repo key you choose); `<name>/<tag>/<asset>`
are the generic store's `<name>/<version>/<filename>` triple. Content under one
`<ns>` is isolated from another (the `_hosted` segment sets the storage
namespace). This is verified against a live server (PUT → 201, GET → 200,
cross-namespace isolated).
