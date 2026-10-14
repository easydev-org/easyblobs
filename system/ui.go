package system

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The SPA (Svelte 5 + Vite + Tailwind, built by the easyblobs-webui repo (https://github.com/easydev-org/easyblobs-webui) → ./ui/dist) is
// embedded so the binary serves it with no external files. `all:` includes the
// Vite hashed assets. The build is committed, so `go build` needs no node.
//
//go:embed all:ui/dist
var uiDist embed.FS

// ui serves the embedded SPA at /artifacts/system/ui/ (mounted by the adapter
// router under /artifacts/system). The SPA itself carries no secrets — the
// operator pastes a write token that only the JSON data endpoint validates — so
// it is served BEFORE the admin gate and can load pre-auth. Unknown sub-paths
// fall back to index.html (client-side routing).
func (s *State) ui(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(uiDist, "ui/dist")
	p := strings.TrimPrefix(r.URL.Path, "/artifacts/system")
	p = strings.TrimPrefix(p, "/system")
	p = strings.TrimPrefix(p, "/ui")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		p = "index.html"
	}
	if st, err := fs.Stat(sub, p); err != nil || st.IsDir() {
		p = "index.html"
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFileFS(w, r, sub, p)
}
