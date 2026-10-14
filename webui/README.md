# artifact webui

Svelte 5 + Vite + Tailwind v4 admin SPA for the artifact registry, mirroring
`easy-vcs/agent-webui`'s stack.

## Build

The built SPA is **committed** under `../system/ui/dist/` and embedded into the
binary via `//go:embed all:ui/dist` (see `../system/ui.go`) — so `go build`
needs no node. Rebuild + commit `dist/` whenever this source changes:

```sh
cd webui
npm install
npm run build        # → ../system/ui/dist (base = /artifacts/system/ui/)
```

## Serving

The `system` adapter serves it at **`/artifacts/system/ui/`** (unauthenticated —
the SPA carries no secrets). It polls the operator-gated data endpoint
`GET /artifacts/system/downloads` with a write-level token the operator pastes
(kept in `localStorage`).

## Pages

- **active downloads** — in-flight upstream fetches (HF LFS, maven, pub, OCI
  layers, …) with bytes/total, percent, rate and elapsed time.
