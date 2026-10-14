package hex

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// search merges local packages with hex.pm's package search. hex.pm returns
// an array of package objects; local packages are prepended (deduped by name).
func (s *State) search(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var out []any
	seen := map[string]bool{}
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "hex")
	q := strings.ToLower(r.URL.Query().Get("search"))
	for _, name := range repos {
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "hex", name)
		out = append(out, map[string]any{
			"name": name, "latest_version": easyblobkit.HighestVersion(vs),
			"downloads": 0, "html_url": "https://hex.pm/packages/" + name,
		})
		seen[name] = true
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "hex", Sub: "api"}); err == nil {
		p := "/api/packages"
		if raw := r.URL.RawQuery; raw != "" {
			p += "?" + raw
		}
		if body, err := remote.GetBytes(r.Context(), p); err == nil {
			var up []map[string]any
			if json.Unmarshal(body, &up) == nil {
				for _, m := range up {
					if n, _ := m["name"].(string); n != "" && !seen[n] {
						out = append(out, m)
					}
				}
			}
		}
	}
	if out == nil {
		out = []any{}
	}
	easyblobkit.JSON(w, http.StatusOK, out)
}

func (s *State) proxyAll(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		easyblobkit.Error(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	upPath := strings.TrimPrefix(path, "pkgs/hex/")
	upPath = strings.TrimPrefix(upPath, "hex/")
	// Preserve the query string: hex's search and paginated endpoints
	// (/api/packages?search=..., /api/packages?page=...) are query-driven.
	if q := r.URL.RawQuery; q != "" {
		upPath += "?" + q
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "hex"}); err == nil {
		if body, err := remote.GetBytes(r.Context(), "/"+strings.Trim(upPath, "/")); err == nil {
			easyblobkit.OctetResponse(w, r, body)
			return
		}
	}
	easyblobkit.Error(w, http.StatusBadGateway, "upstream")
}
