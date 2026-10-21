package system

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The SPA (Svelte 5 + Vite + Tailwind, from the easyblobs-webui repo,
// github.com/easydev-org/easyblobs-webui) is embedded so the binary serves it
// with no external files. `all:` includes the Vite hashed assets.
//
// ui/dist is built by the Containerfile's node stage and is NOT committed, so
// the directory may be ABSENT in a source checkout. `go:embed` fails the BUILD
// in that case, which used to make `go build ./...` and `go test ./...` fail
// before the Docker build ever ran. Instead of embedding a non-existent tree we
// keep a committed placeholder (ui/dist/.gitkeep) so the pattern always
// matches; when the real build runs, its files are picked up.
//
//go:embed all:ui/dist
var uiDist embed.FS

// hasUI reports whether the embedded SPA is actually present (the placeholder
// alone means it is not). Without it we serve a short plain-text page instead of
// a confusing 404, so a source checkout still answers /artifacts/system/ui.
func hasUI(sub fs.FS) bool {
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return false
	}
	return true
}

// ui serves the embedded SPA at /artifacts/system/ui/ (mounted by the adapter
// router under /artifacts/system). The SPA itself carries no secrets — the
// operator pastes a write token that only the JSON data endpoint validates — so
// it is served BEFORE the admin gate and can load pre-auth. Unknown sub-paths
// fall back to index.html (client-side routing).
func (s *State) ui(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(uiDist, "ui/dist")
	// No real SPA (source checkout: only the placeholder is embedded). Answer
	// with a short page instead of a 404 so /artifacts/system/ui is never a dead
	// end, and the JSON admin API stays the source of truth.
	if !hasUI(sub) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("easyblobs system UI is not built into this binary.\n" +
			"Use the JSON endpoints under /artifacts/system, or build the webui " +
			"(npm run build) and rebuild the image.\n"))
		return
	}
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
