# artifact webui — design

An operator console for the artifact registry (JFrog/Nexus-style), served by the
`system` adapter at **`/artifacts/system/ui/`** (embedded via `//go:embed`,
built from this directory into `../system/ui/dist`).

Stack: Svelte 5 + Vite + Tailwind v4, the SAME design tokens as
`easy-vcs/agent-webui` (see `src/app.css`), shadcn-svelte-style components
(`src/lib/components/ui/*`) and `@lucide/svelte` icons. Same-origin; the
operator pastes a write-level token (localStorage), which only the data
endpoints validate.

## API surface

| Endpoint | Use |
|---|---|
| `GET /artifacts/system/stats` | per-format rows / blobs / bytes / expired |
| `GET /artifacts/system/packages` | all packages (format, repository, version, size, source, hosted, namespace, repo) |
| `DELETE /artifacts/system/packages?repo=` | delete a repository's packages |
| `GET /artifacts/system/repos` | upstream repo key → url |
| `GET /artifacts/system/targets` | targets (protocol/kind/base/hosts/auth/builtin) |
| `GET /artifacts/system/upstreams` | upstream url overrides |
| `GET /artifacts/system/proxy` | per-key proxy policy |
| `GET /artifacts/system/downloads` | in-flight upstream downloads (progress) |
| `GET /v2/_catalog[?owner=]` | OCI images + tags |

## Pages

1. **Overview** — totals (packages / blobs / bytes), per-format breakdown,
   hosted vs cached split, active-download count.
2. **Packages** — filter by format / namespace / repo + free search; columns
   `package / version / origin (hosted|cached) / size / install`; copy the
   install command; delete a repository's packages.
3. **Images (OCI)** — catalog by owner; tags per image; copy `docker pull`.
4. **Repositories** — upstream repo mapping (key → URL) and hosted namespaces.
5. **Downloads** — in-flight upstream fetches with a live progress bar.
6. **Config** — upstreams / targets / proxy (read-only).

## Build

```sh
cd webui && npm install && npm run build   # → ../system/ui/dist (committed)
```
