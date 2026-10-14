package npm

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) allPackages(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "npm")
	entries := map[string]any{}
	for _, repo := range repos {
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "npm", repo)
		latest := easyblobkit.HighestVersion(versions)
		if latest == "" && len(versions) > 0 {
			latest = versions[len(versions)-1]
		}
		entries[repo] = map[string]any{"name": repo, "latest": latest}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"db_name": "registry", "_updated": 0, "entries": entries})
}

func (s *State) securityStub(w http.ResponseWriter, r *http.Request, path string) {
	if strings.Contains(path, "/quick") {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"secret": "audits-quick"})
		return
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{})
}

func (s *State) tokens(w http.ResponseWriter, r *http.Request, method string) {
	switch method {
	case http.MethodDelete:
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPost:
		easyblobkit.JSON(w, http.StatusCreated, map[string]any{"token": "npm-anonymous", "key": "npm-anonymous"})
	default:
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"objects": []any{}, "total": 0})
	}
}

func (s *State) couchUser(w http.ResponseWriter, r *http.Request, path, method string) {
	name := strings.TrimPrefix(path, "-/user/")
	// Strip "org.couchdb.user:" prefix if present.
	name = name[strings.LastIndex(name, ":")+1:]
	switch method {
	case http.MethodPut, http.MethodPost:
		easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true, "name": name, "token": "npm-anonymous", "id": "org.couchdb.user:" + name, "rev": "1"})
	case http.MethodDelete:
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"_id": path, "name": path})
	}
}

func (s *State) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(r.URL.Query().Get("text"), "%20", " "), "+", " "))
	var objects []any
	seen := map[string]bool{}
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "npm")
	for _, repo := range repos {
		if q != "" && !strings.Contains(strings.ToLower(repo), q) {
			continue
		}
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "npm", repo)
		latest := easyblobkit.HighestVersion(versions)
		if latest == "" && len(versions) > 0 {
			latest = versions[len(versions)-1]
		}
		objects = append(objects, map[string]any{"package": map[string]any{"name": repo, "version": latest}})
		seen[repo] = true
	}
	// Merge the upstream search so uncached public packages are discoverable.
	if remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "npm"}); remote != nil {
		p := "/-/v1/search"
		if raw := r.URL.RawQuery; raw != "" {
			p += "?" + raw
		}
		if body, err := remote.GetBytes(r.Context(), p); err == nil {
			var up struct {
				Objects []map[string]any `json:"objects"`
			}
			if json.Unmarshal(body, &up) == nil {
				for _, o := range up.Objects {
					if pkg, ok := o["package"].(map[string]any); ok {
						if n, _ := pkg["name"].(string); n != "" && !seen[n] {
							objects = append(objects, o)
						}
					}
				}
			}
		}
	}
	if objects == nil {
		objects = []any{}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"objects": objects, "total": len(objects), "time": ""})
}
