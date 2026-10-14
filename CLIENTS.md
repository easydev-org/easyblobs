# CLIENTS — pointing each client at artifact

`artifact` is a **pull-through + hosted** package registry. This document tells
you, per protocol:

1. **how to point the client at artifact** (the explicit URL to configure), and
2. **how a self-published ("hosted") package is distinguished from a cached
   upstream package**.

See `ARCHITECTURE.md` for the internals. The authoritative, executable examples
live under `e2e/scripts/<proto>.sh` (pull-through) and
`e2e/lifecycle/scripts/<proto>.sh` (publish → consume → upgrade → delete); this
document mirrors them.

> This file is the authoritative **client** (consume) setup for every protocol.
> For the platform-wide policy — **publish to / consume from easyblobs instead of
> GitHub or public registries** — see `easy-vcs/deploy:PUBLISHING.md`.

---

## 1. The addressing model (no sidecar)

There is **no intercepting sidecar**. Clients are pointed **straight at artifact's
explicit URL**; artifact itself fetches the upstream (through the cluster proxy).

- Non-OCI protocols are mounted at **`/artifacts/<proto>/...`**.
- **OCI** is mounted at **`/v2`** (spec-fixed); a docker/podman client carries
  the registry in the **Host header**, never in the path.
- A **named mirror** is not a mount of its own: reach it with the reserved
  `_upstream/<host>` segment, e.g. `/artifacts/maven/_upstream/dl.google.com/...`,
  `/artifacts/npm/_upstream/npm.jsr.io/...` (see §6).

> Historically a DNS-spoofing + TLS-MITM egress sidecar made upstream hostnames
> resolve to the gateway so an unmodified client was served transparently. That
> layer has been **removed**; the client is now responsible for pointing at the
> mount. The e2e harness (`e2e/`) reflects the new model.

---

## 2. Quick start

```
choose protocol  →  point the client at http://<artifact-host>/artifacts/<proto>/…
                 →  install; on a cache miss artifact pulls the upstream and caches it
                 →  publish (if supported) to the same mount; it is served locally
```

`<artifact-host>` is the host:port you reach artifact by (an in-cluster Service
like `artifact.temp.svc.cluster.local:80`, or a gateway address). Examples below
write it as `http://artifact/…` for brevity.

---

## 3. Hosted vs upstream: three cases

Whether a self-published package is distinguishable from a cached upstream
package depends on **whether the protocol has a namespace of its own**.

| Case | Protocols | Namespace | Hosted form | Upstream form | Extra config? |
|---|---|---|---|---|---|
| **A. Protocol has a namespace (one URL auto-splits)** | npm, maven, go, oci | `@scope` / groupId / module path / registry host | `@acme/tool`, `io.acme/tool`, `example.com/tool`, `ghcr.io/acme/app` | `left-pad`, `org.slf4j/slf4j-api`, `golang.org/x/text`, `docker.io/library/alpine` | **No** — use your own scope/groupId/module path |
| **B. No namespace; first segment is the repo key** | pypi, rubygems, cargo, composer, hex, pub, nuget, helm, conan, nix, swift, huggingface, protobuf, ivy, git, gitlfs, netcache | first path segment | shares the same key as upstream by default | shares the same key as upstream by default | **Yes**, to force separation (see §6) |
| **C. Flat-repo protocols (reserved `_hosted` segment)** | apk, debian, rpm, conda | first segment = repo/channel key | `/artifacts/<proto>/_hosted/<repo>/…` | `/artifacts/<proto>/<repo>/…` | **No** — already separated |

### Case A — the protocol itself splits hosted from upstream

The namespace is part of the package identity, so "your" packages are already
distinguishable by prefix, and both kinds are served from the **same URL**:

- **npm** — the scope (`@acme/tool`) is the namespace. `npm publish` **requires a
  scope**: an unscoped name is rejected outright (`403`, "scoped package names
  (@scope/name) are required on this registry" — see `npm/lib.go`
  `authorizePublish`). So every self-published npm package necessarily carries an
  explicit `@scope` prefix; upstream packages are plain names.
- **maven** — the groupId is the namespace. Use your own groupId
  (e.g. `io.acme.*`) for hosted artifacts; upstream artifacts keep theirs
  (`org.slf4j`, `com.google.guava`).
- **go** — the module path is the namespace. Hosted modules use your domain
  (`example.com/tool`); upstream modules keep theirs (`golang.org/x/text`).
- **oci** — the registry host is the namespace. See §4 (OCI is special: the host
  is the registry, and it lives in the Host header, not the path).

### Case B — no namespace; hosted and upstream share the repo key by default

These protocols have no scope concept. artifact stores content under the
**first path segment as the repository key**, and for these formats **the local
(hosted) content and the cached upstream content live under the same key and are
merged on read**:

- **pypi**: `GET /artifacts/pypi/simple/<name>/` merges local files with the
  upstream index, so a locally published project and a same-named upstream
  project appear on one page (`pypi/lib.go` `simpleProject`).
- **maven**: `GET /artifacts/maven/<group>/<artifact>/<version>/<file>` looks in
  the local index first and falls back to the upstream (`maven/lib.go` `getPath`).

To *force* a separation you use a convention or the explicit repository segment
(§6). The `/-/<repo>/` marker and the `_hosted` segment are the two mechanisms.

### Case C — flat-repo protocols use `_hosted`

`apk`, `debian`, `rpm`, `conda` address content by a repo/channel key that is
**also** the upstream path segment (e.g. apt's `deb.debian.org/debian/...`). A
self-published repo and a cached upstream repo could otherwise collide under the
same key, so artifact reserves a segment:

```
/artifacts/<proto>/<repo>/…            → pull-through cache ONLY (never hosted)
/artifacts/<proto>/_hosted/<repo>/…    → self-published (hosted) ONLY
```

A plain path is **never probed** for hosted content, and a `_hosted` path is
**never** proxied: the two are strictly separate (`core/hosted.go`).

---

## 4. Per-protocol client configuration

> All examples assume the client can reach `http://artifact` (your artifact
> host). The e2e scripts referenced next to each entry are the authoritative,
> working examples.

### npm
```ini
# .npmrc  (project root or ~/.npmrc)
registry=http://artifact/artifacts/npm/
//artifact/artifacts/npm/:_authToken=${NPM_TOKEN}
```
- Install (pull-through + hosted): `npm install` — both kinds come from the one
  `registry` URL.
- Publish: `npm publish --access public` with a **scoped** name (`@acme/tool`);
  unscoped publish is rejected.
- The token line's key must match the `registry` URL exactly (npm matches
  credentials by URL).
- Ref: `e2e/scripts/npm.sh`, `e2e/lifecycle/scripts/npm.sh`.

### PyPI
```sh
pip install --index-url http://artifact/artifacts/pypi/simple/ <pkg>
# publish:
twine upload --repository-url http://artifact/artifacts/pypi/upload -u <user> -p <token> dist/*
```
- Ref: `e2e/scripts/pypi.sh`, `e2e/lifecycle/scripts/pypi.sh`.

### Maven
```xml
<!-- ~/.m2/settings.xml -->
<settings>
  <mirrors>
    <mirror>
      <id>artifact</id>
      <url>http://artifact/artifacts/maven/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
```
- Deploy: set the `<distributionManagement>` `<repository>` `<url>` to
  `http://artifact/artifacts/maven/`.
- Ref: `e2e/scripts/maven.sh`, `e2e/lifecycle/scripts/maven.sh`.

### Go
```sh
GOPROXY=http://artifact/artifacts/go GOSUMDB=off go get <module>@<version>
# publish a module:
curl -X PUT --data-binary @module.zip \
  "http://artifact/artifacts/go/upload?name=<module>&version=v1.0.0"
```
- Ref: `e2e/scripts/go.sh`, `e2e/lifecycle/scripts/go.sh`.

### Cargo
```toml
# $CARGO_HOME/config.toml
[source.crates-io]
replace-with = "artifact"

[source.artifact]
registry = "sparse+http://artifact/artifacts/cargo/index/"
```
- Ref: `e2e/scripts/cargo.sh`, `e2e/lifecycle/scripts/cargo.sh`.

### NuGet
```xml
<!-- ~/.nuget/NuGet/NuGet.Config -->
<configuration>
  <packageSources>
    <clear />
    <add key="artifact" value="http://artifact/artifacts/nuget/v3/index.json"
         allowInsecureConnections="true" />
  </packageSources>
</configuration>
```
- `allowInsecureConnections` is required when artifact is served over plain HTTP.
- Ref: `e2e/scripts/nuget.sh`, `e2e/lifecycle/scripts/nuget.sh`.

### RubyGems
```sh
gem sources --clear-all
gem sources --add http://artifact/artifacts/rubygems/
gem install <gem>
```
- Ref: `e2e/scripts/rubygems.sh`, `e2e/lifecycle/scripts/rubygems.sh`.

### Composer
```sh
composer config -g secure-http false
composer config -g repos.packagist composer http://artifact/artifacts/composer/
```
- Ref: `e2e/scripts/composer.sh`, `e2e/lifecycle/scripts/composer.sh`.

### Hex (Elixir/Erlang)
```sh
export HEX_MIRROR=http://artifact/artifacts/hex/
mix deps.get
```
- Ref: `e2e/scripts/hex.sh`, `e2e/lifecycle/scripts/hex.sh`.

### pub (Dart)
```sh
export PUB_HOSTED_URL=http://artifact/artifacts/pub
dart pub get
```
- Ref: `e2e/scripts/pub.sh`, `e2e/lifecycle/scripts/pub.sh`.

### Helm
```sh
helm repo add artifact http://artifact/artifacts/helm
helm repo update
helm pull artifact/<chart>
```
- Ref: `e2e/scripts/helm.sh`, `e2e/lifecycle/scripts/helm.sh`.

### Conan
```sh
conan remote add artifact http://artifact/artifacts/conan
conan download <ref> -r artifact
```
- Ref: `e2e/scripts/conan.sh`, `e2e/lifecycle/scripts/conan.sh`.

### Conda
```sh
conda create -c http://artifact/artifacts/conda/pkgs/main <pkg>
# self-published channel:
conda create -c http://artifact/artifacts/conda/_hosted/<repo> <pkg>
```
- Ref: `e2e/scripts/conda.sh`, `e2e/lifecycle/scripts/conda.sh`.

### Debian / apt
```
# cached upstream archive (first segment = archive key "debian"):
deb [trusted=yes] http://artifact/artifacts/debian/debian bookworm main

# self-published repo (hosted):
deb [trusted=yes] http://artifact/artifacts/debian/_hosted/<repo> ./
```
- Ref: `e2e/scripts/debian.sh`, `e2e/lifecycle/scripts/debian.sh`.

### Alpine / apk
```
# /etc/apk/repositories — cached upstream (first segment is the repo key):
http://artifact/artifacts/apk/v3.24/main
http://artifact/artifacts/apk/v3.24/community

# self-published repo (hosted):
http://artifact/artifacts/apk/_hosted/<repo>/<arch>
```
- Ref: `e2e/scripts/apk.sh`, `e2e/lifecycle/scripts/apk.sh`.

### Fedora / rpm
```ini
# /etc/yum.repos.d/<name>.repo — cached upstream:
[fedora]
name=fedora
baseurl=http://artifact/artifacts/rpm/pub/fedora/linux/releases/43/Everything/x86_64/os/
enabled=1
gpgcheck=0
```
```
# self-published repo (hosted):
baseurl=http://artifact/artifacts/rpm/_hosted/<repo>/<arch>
```
- Ref: `e2e/scripts/rpm.sh`, `e2e/lifecycle/scripts/rpm.sh`.

### OCI (docker/podman/skopeo)
The docker/podman client carries the registry in the **Host header**, never the
path, so the easyblobs Service host **is** the registry for that client. The
reference form is `docker://<host>/<repo>:<tag>` — the `<host>` is the registry,
and the client appends `/v2/…` itself, so **do not** put `/v2` in the reference:

```sh
skopeo inspect --tls-verify=false docker://artifact/<repo>:<tag>
skopeo copy    --tls-verify=false docker://<upstream-host>/<img> docker://artifact/<repo>:<tag>
```

(`artifact` here is the easyblobs Service host; `<repo>` follows the host
directly, e.g. `docker://artifact/library/busybox:latest` requests
`GET /v2/library/busybox/manifests/latest`.)

- **Docker Hub** can be mirrored with `registry-mirrors` (the client rewrites the
  Hub host to the mirror).
- **Other registries** (ghcr.io, quay.io, …) have no configurable base URL, so
  each needs its **own Service host** (the host **is** the registry; e.g. a
  Service `ghcr-mirror` reached as `docker://ghcr-mirror/<repo>:<tag>`) or an
  explicit named target — you cannot put the registry in the path.
- Ref: `e2e/scripts/oci.sh`, `e2e/lifecycle/scripts/oci.sh`, and the "Notes /
  gotchas" section of `e2e/harness/README.md`.

### git / git-lfs
```sh
git clone http://artifact/artifacts/git/<host>/<owner>/<repo>.git
# or rewrite transparently:
git config --global url."http://artifact/artifacts/git/github.com/".insteadOf https://github.com/
```
- LFS objects are served by the same adapter from the CAS.
- Ref: `e2e/scripts/git.sh`, `e2e/scripts/gitlfs.sh`.

### netcache (arbitrary URL cache)
```
http://artifact/artifacts/netcache/<host>/<path>?<query>
```
- The host is always the first path segment (no transparent mode). Range requests
  are served from the CAS.
- Ref: `e2e/scripts/netcache.sh`.

### ivy
```
http://artifact/artifacts/ivy/<host>/<path>
```
- The upstream host is the first path segment (e.g. `repo.scala-sbt.org`).
- Ref: `e2e/scripts/ivy.sh`.

### Buf Schema Registry (protobuf)

The BSR is **not** a REST registry: it is a set of Protobuf RPC services exposed
over **Connect** (and gRPC / gRPC-Web), with every method mounted at the **host
root**:

```
POST https://buf.build/<fully.qualified.ServiceName>/<MethodName>   (Connect JSON)
```

artifact proxies that root verbatim, so point a Connect/`buf` client's BSR base
URL at artifact:

```
POST http://artifact/artifacts/protobuf/<Service>/<Method>
```

e.g. `POST http://artifact/artifacts/protobuf/buf.registry.module.v1.ModuleService/GetModules`
with a Connect JSON body. Bodies are cached; the `Authorization` header (your
BSR token) is forwarded upstream. Only `POST` is proxied (Connect unary).

- **Generated SDKs** are served by their own ecosystems, not this mount: Go SDKs
  (`buf.build/gen/go/...`) via the **Go proxy**, npm SDKs (`@buf/...`) via the
  **npm registry**.
- Ref: `e2e/scripts/protobuf.sh`.

## 5. Point every protocol at ONE artifact instance

§4 shows each protocol on its own. This section is the **copy-paste checklist**:
replace `<ARTIFACT>` with your instance's base URL — e.g. the shared all-protocol
upstream `http://artifact-pvc.worker.svc.cluster.local` — and paste the line for
each ecosystem your repo/sandbox uses. On a cache miss artifact pulls the
upstream once and caches it; later requests are served locally. A shared upstream
is reached over plain HTTP, so client flags that need that (e.g. pip's
`--trusted-host`) are noted inline.

> **Credentials.** *Pull is anonymous* on a shared instance, so nothing below
> needs a token to **install**. A **push** needs a write-level credential, and
> **`/v2/_catalog`** (the OCI browse endpoint) needs *any* valid credential —
> where a token is needed the line shows `<token>`; never hard-code a real token.
> Quick check: `curl -o /dev/null -w '%{http_code}\n' <ARTIFACT>/v2/token` — a
> **200** means the instance is in **closed** (auth-enabled) mode.
>
> These are *client* settings and are independent of the hosted-vs-upstream
> distinction in §3/§6: pointing at one instance means self-published packages
> (npm `@scope`, the `_hosted` segment, …) and cached upstream packages coexist
> under the same URL.

```ini
# npm — .npmrc (project root or ~/.npmrc)
registry=<ARTIFACT>/artifacts/npm/
# optional, only to push: the key must match the registry URL exactly
//<host>/artifacts/npm/:_authToken=<token>
```

```sh
# PyPI
pip install --index-url <ARTIFACT>/artifacts/pypi/simple/ \
            --trusted-host <host> <pkg>
# publish:
twine upload --repository-url <ARTIFACT>/artifacts/pypi/upload -u <user> -p <token> dist/*
```

```sh
# Go
GOPROXY=<ARTIFACT>/artifacts/go GOSUMDB=off go get <module>@<version>
# publish a module:
curl -X PUT --data-binary @module.zip \
  "<ARTIFACT>/artifacts/go/upload?name=<module>&version=v1.0.0"
```

```toml
# Cargo — $CARGO_HOME/config.toml
[source.crates-io]
replace-with = "artifact"

[source.artifact]
registry = "sparse+<ARTIFACT>/artifacts/cargo/index/"
```

```xml
<!-- Maven — ~/.m2/settings.xml -->
<settings>
  <mirrors>
    <mirror>
      <id>artifact</id>
      <url><ARTIFACT>/artifacts/maven/</url>
      <mirrorOf>*</mirrorOf>
    </mirror>
  </mirrors>
</settings>
```

```xml
<!-- NuGet — ~/.nuget/NuGet/NuGet.Config -->
<configuration>
  <packageSources>
    <clear />
    <add key="artifact" value="<ARTIFACT>/artifacts/nuget/v3/index.json"
         allowInsecureConnections="true" />
  </packageSources>
</configuration>
```

```sh
# RubyGems
gem sources --clear-all
gem sources --add <ARTIFACT>/artifacts/rubygems/
gem install <gem>
```

```sh
# Composer
composer config -g secure-http false
composer config -g repos.packagist composer <ARTIFACT>/artifacts/composer/
```

```sh
# Hex (Elixir/Erlang)
export HEX_MIRROR=<ARTIFACT>/artifacts/hex/
mix deps.get
```

```sh
# pub (Dart)
export PUB_HOSTED_URL=<ARTIFACT>/artifacts/pub
dart pub get
```

```sh
# Helm
helm repo add artifact <ARTIFACT>/artifacts/helm
helm repo update
helm pull artifact/<chart>
```

```sh
# Conan
conan remote add artifact <ARTIFACT>/artifacts/conan
conan download <ref> -r artifact
```

```sh
# Conda
conda tos accept --override-channels --channel <ARTIFACT>/artifacts/conda/pkgs/main >/dev/null 2>&1 || true
conda create -y -n <env> --override-channels -c <ARTIFACT>/artifacts/conda/pkgs/main <pkg>
```

```
# Debian / apt — /etc/apt/sources.list.d/artifact.list
# The first path segment after /artifacts/debian/ is the ARCHIVE KEY and must be
# literally "debian" (or "ubuntu", "debian-security", ...); then the suite.
deb [trusted=yes] <ARTIFACT>/artifacts/debian/debian <suite> main
# e.g. <suite> = bookworm | trixie
```

```
# Alpine / apk — /etc/apk/repositories  (first segment is the repo key)
<ARTIFACT>/artifacts/apk/v3.24/main
<ARTIFACT>/artifacts/apk/v3.24/community
```

```ini
# Fedora / rpm — /etc/yum.repos.d/<name>.repo
[fedora]
name=fedora
baseurl=<ARTIFACT>/artifacts/rpm/pub/fedora/linux/releases/<ver>/Everything/<arch>/os/
enabled=1
gpgcheck=0
optional_metadata_types=primary
# e.g. <ver>=43, <arch>=x86_64
```

```sh
# Nix — point the binary-cache store at artifact
nix store info --store <ARTIFACT>/artifacts/nix
```

```
# OCI — the registry travels in the Host header, never the path, so the
# <ARTIFACT> HOST *is* the registry for docker/podman/skopeo:
#   skopeo inspect --tls-verify=false docker://<artifact-host>/<repo>:<tag>
# Docker Hub: set `registry-mirrors` = <artifact-host>.
# Other registries (ghcr.io, quay.io, ...): give each its OWN Service host (the
# host is the registry) or a named target — the registry cannot go in the path.
# See §4 "OCI" and e2e/harness/README.md.
```

```sh
# git / git-lfs — the upstream host is the first path segment
git clone <ARTIFACT>/artifacts/git/<host>/<owner>/<repo>.git
# or rewrite transparently:
git config --global url."<ARTIFACT>/artifacts/git/github.com/".insteadOf https://github.com/
```

```
# ivy — upstream host is the first path segment
<ARTIFACT>/artifacts/ivy/<host>/<path>
# e.g. <ARTIFACT>/artifacts/ivy/repo.scala-sbt.org/scalasbt/sbt-plugin-releases/...
```

```
# netcache — arbitrary URL cache; the host is the first path segment
<ARTIFACT>/artifacts/netcache/<host>/<path>?<query>
```

```sh
# HuggingFace Hub — point the endpoint at artifact
export HF_ENDPOINT=<ARTIFACT>/artifacts/huggingface
```

> **Xet is not brokered.** The Hub advertises the Xet transfer protocol via
> `rel="xet-auth"` / `rel="xet-reconstruction-info"` links pointing at absolute
> `huggingface.co` / `cas-server.xethub.hf.co` URLs. artifact **strips those
> links** so `huggingface_hub` falls back to the classic LFS/CDN path (an
> absolute 302 that artifact passes through). If your client still tries Xet,
> set `HF_HUB_DISABLE_XET=1`.

> **LFS weights are cached server-side (default).** When the Hub answers a
> resolve request with a 307 to the CDN (LFS-backed files), artifact **follows
> it and streams the bytes into its CAS**, then serves them from local storage —
> so a repeat pull does not hit the public CDN and a sandbox without public
> egress can still fetch weights. Set `EASYBLOBS_HF_DISABLE_LFS_CACHE=1` to
> restore the old pass-the-redirect-through behaviour (client fetches the CDN
> directly).

Each line above mirrors the corresponding executable example in
`e2e/scripts/<proto>.sh` (the authoritative, tested form).

---

## 6. Explicit repository segment `/-/<repo>/` and `_hosted`

Two mechanisms exist to make "hosted" explicit for protocols that do not carry a
namespace of their own.

### `/-/<repo>/` — explicit repository, all protocols

Any protocol accepts an explicit repository marker:

```
/artifacts/npm/-/@acme/ui          → repo @acme, path ui
/artifacts/pypi/-/internal/simple  → repo internal, path simple
```

It is rewritten to the adapter's native path with the repository carried in the
request scope, so the adapter sees its normal URL shape while the storage key is
qualified by the repository. This is meant for **CI / publish scripts** that want
to drop a self-published package into a specific repository, physically separate
from the pull-through cache. (A stock client does not emit this form; you use it
from your own automation.) See `core/namespace.go` (`ExplicitRepoMarker`) and
`core/scope_middleware.go`.

There is also a `?repository=<repo>` query form used by flat publish paths.

### `_upstream/<host>/` and `_repo/<repo>/` — the reserved-segment grammar

The reserved `_<kind>` segments form one addressing grammar (first segment is
always the protocol, so a host shared by several protocols — `buf.build`,
`github.com` — is disambiguated by construction):

```
/artifacts/<proto>/<tail>                       default upstream
/artifacts/<proto>/_upstream/<host>/<tail>      explicit upstream (host)
/artifacts/<proto>/_repo/<repo>/<tail>          explicit repository
/artifacts/<proto>/_hosted/<repo>/<tail>        self-published repository
/artifacts/<proto>/-/<repo>/<tail>              legacy explicit repository
```

- **`_upstream/<host>`** fetches from `<host>` for this request: a **declared**
  target of that protocol keeps its base path and auth (e.g.
  `/artifacts/maven/_upstream/dl.google.com/…` → `dl.google.com/dl/android/maven2`);
  any other **public** host is used as `https://<host>`. Non-public hosts
  (cluster-local, bare, IP) are rejected by the SSRF guard. Only the host is in
  the path — never a wildcard.
- **`_repo/<repo>`** is `/-/<repo>` under the reserved-segment family.

See `core/upstream_segment.go`, `core/scope_middleware.go`.

### `_hosted` — flat-repo protocols only

For **apk / debian / rpm / conda** the reserved `_hosted/<repo>/` segment is the
only way to separate a self-published repo from a cached upstream repo:

```
/artifacts/<proto>/_hosted/<repo>/…    hosted only
/artifacts/<proto>/<repo>/…            cache only
```

See `core/hosted.go` (`HostedSegment`, `SplitHosted`, `HostedRequest`).

### Releases & arbitrary assets → the `generic` store

There is no release model in the VCS; **release assets, build outputs, and any
non-package blob** go to the `generic` store, keyed by the owning namespace so
it lines up with the OCI org:

```
PUT/GET/DELETE /artifacts/generic/_hosted/<ns>/<name>/<tag>/<asset>
```

`<name>/<tag>/<asset>` is the generic store's `<name>/<version>/<filename>`
triple; content under one `<ns>` is isolated from another. See
`docs/agent-gateway-integration.md` §10.

Deleting is **per file**: `DELETE /artifacts/generic/<name>/<version>/<filename>`
(write credential) removes just that file's descriptor and drops its CAS blob
only when nothing else references the digest — the other files of the same
version survive. (`DELETE /artifacts/generic/<name>.version` clears the whole
name's index, which is broader than a single file.)

---

## 7. FAQ

**Why does npm require a scope?**
An unscoped global name cannot be claimed by a single tenant, which would allow
squatting on public-name look-alikes. So `npm publish` rejects unscoped names
(403); every hosted npm package is `@scope/name`, while upstream packages are
plain names. This makes hosted vs upstream self-evident in the package name.

**Why do local and upstream pypi content merge?**
A pull-through mirror must present the *full* index: if a locally cached wheel
shadowed the rest of a project's files, resolvers (pip/uv) that resolve version
ranges against the whole page would break. So `/simple/<name>/` always merges
local + upstream (`pypi/lib.go`). If you need a hosted project to be strictly
separate, publish it under an explicit repository (`/-/<repo>/`, §6) or a
distinct name.

**Why is OCI special?**
A docker/podman client sends the registry in the **Host header** (and TLS SNI),
never in the path. So the client cannot be pointed at "one base URL per
registry"; each registry needs its own host, and the host is the namespace.
Docker Hub is the easy case (`registry-mirrors`); other registries need a
distinct Service host or a named target.

**Which protocols are already separated, with no extra config?**
npm, maven, go, oci (by namespace — Case A) and apk, debian, rpm, conda (by
`_hosted` — Case C).

**Which need a convention or explicit repository?**
pypi, rubygems, cargo, composer, hex, pub, nuget, helm, conan, nix, swift,
huggingface, protobuf, ivy, git, gitlfs, netcache (Case B).

---

## 8. Reference — where this is implemented

| Concept | Source |
|---|---|
| Namespace resolvers (per format) | `core/namespace.go` — `ScopeNamespace` (npm), `MavenNamespace`, `GoModuleNamespace`, `OCIHostNamespace`, `FirstSegNamespace` (apk/debian/rpm/conda/ivy), `FlatNamespace` (default) |
| Explicit repository marker `/-/<repo>/` | `core/namespace.go` (`ExplicitRepoMarker`), `core/scope_middleware.go` |
| Request scope & key encoding | `core/scope.go`, `core/scoped_store.go` |
| Hosted (`_hosted`) support | `core/hosted.go` |
| Target mounts / routing | `core/target_dispatch.go`, `targets/` |
| npm scope requirement | `npm/lib.go` (`authorizePublish`) |
| pypi local+upstream merge | `pypi/lib.go` (`simpleProject`) |
| maven local-then-upstream | `maven/lib.go` (`getPath`) |
| OCI host handling | `core/registry_host.go`, `oci/adapter.go` |
| Executable client examples | `e2e/scripts/<proto>.sh`, `e2e/lifecycle/scripts/<proto>.sh` |
| E2E harness overview | `e2e/harness/README.md` |

---

*This document mirrors the e2e scripts and the source at the time of writing;
the source is authoritative for the latest behavior.*
