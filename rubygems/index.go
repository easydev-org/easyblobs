package rubygems

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) names(w http.ResponseWriter, r *http.Request) {
	// The compact index's /names lists every gem; `gem search` reads it, so a
	// local-only list would hide the whole public registry. Merge the upstream
	// names (cached) beneath the local ones.
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "rubygems")
	seen := map[string]bool{}
	var names []string
	for _, n := range repos {
		if !seen[n] {
			names = append(names, n)
			seen[n] = true
		}
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems", Sub: "index"}); err == nil {
		if body, err := remote.GetCached(r.Context(), easyblobkit.SharedIndexCache(), "/names"); err == nil {
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !seen[line] {
					names = append(names, line)
					seen[line] = true
				}
			}
		}
	}
	easyblobkit.Text(w, http.StatusOK, strings.Join(names, "\n"), "text/plain")
}

func (s *State) versionsAPI(w http.ResponseWriter, r *http.Request, name string) {
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
	if len(versions) == 0 {
		remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems"})
		if remote != nil {
			if body, err := remote.GetBytes(r.Context(), "/api/v1/versions/"+easyblobkit.URLencode(name)+".json"); err == nil {
				easyblobkit.JSON(w, http.StatusOK, json.RawMessage(body))
				return
			}
		}
	}
	var out []any
	for _, v := range versions {
		out = append(out, map[string]any{"number": v, "platform": "ruby", "created_at": "2024-01-01T00:00:00Z"})
	}
	easyblobkit.JSON(w, http.StatusOK, out)
}

func (s *State) latest(w http.ResponseWriter, r *http.Request, name string) {
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"version": easyblobkit.HighestVersion(versions)})
}

func (s *State) gemsInfo(w http.ResponseWriter, r *http.Request, name string) {
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
	if len(versions) == 0 {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	var lines []string
	for _, v := range versions {
		lines = append(lines, v+" |sha256:|")
	}
	easyblobkit.Text(w, http.StatusOK, "---\n"+strings.Join(lines, "\n"), "text/plain")
}

func (s *State) gemsList(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "rubygems")
	var out []any
	for _, name := range repos {
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
		out = append(out, map[string]any{"name": name, "version": easyblobkit.HighestVersion(versions)})
	}
	easyblobkit.JSON(w, http.StatusOK, out)
}

func (s *State) owners(w http.ResponseWriter, r *http.Request) {
	easyblobkit.JSON(w, http.StatusOK, []any{map[string]any{"id": 1, "handle": "anonymous", "role": "owner"}})
}

func (s *State) searchGem(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("query"))
	var out []any
	seen := map[string]bool{}
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "rubygems")
	for _, name := range repos {
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
		out = append(out, map[string]any{"name": name, "version": easyblobkit.HighestVersion(versions)})
		seen[name] = true
	}
	// Merge the upstream search so uncached public gems are discoverable.
	if remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems"}); remote != nil {
		p := "/api/v1/search.json"
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

func (s *State) versionV2(w http.ResponseWriter, r *http.Request, path string) {
	// /api/v2/rubygems/{name}/versions/{version}
	rest := strings.TrimPrefix(path, "api/v2/rubygems/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 3 {
		name := strings.TrimSuffix(parts[0], ".json")
		ver := strings.TrimSuffix(parts[2], ".json")
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"name": name, "version": ver, "platform": "ruby", "number": ver, "created_at": "2024-01-01T00:00:00Z"})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *State) compactVersions(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "rubygems")
	var sb strings.Builder
	sb.WriteString("created_at: 2026-01-01T00:00:00Z\n---\n")
	for _, name := range repos {
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
		if len(versions) == 0 {
			continue
		}
		sb.WriteString(name + " " + strings.Join(versions, ",") + " " + strings.Repeat("0", 64) + "\n")
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems", Sub: "index"}); err == nil {
		if body, err := remote.GetCached(r.Context(), easyblobkit.SharedIndexCache(), "/versions"); err == nil {
			if i := strings.Index(body, "---\n"); i >= 0 {
				sb.WriteString(body[i+4:])
			}
		}
	}
	easyblobkit.Text(w, http.StatusOK, sb.String(), "text/plain; version=1")
}

func (s *State) compactInfo(w http.ResponseWriter, r *http.Request, name string) {
	name = strings.Trim(name, "/")
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
	if len(versions) == 0 {
		if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems", Sub: "index"}); err == nil {
			if body, err := remote.GetCached(r.Context(), easyblobkit.SharedIndexCache(), "/info/"+easyblobkit.URLencode(name)); err == nil {
				easyblobkit.Text(w, http.StatusOK, body, "text/plain")
				return
			}
		}
		easyblobkit.Text(w, http.StatusOK, "---\n", "text/plain")
		return
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	for _, v := range versions {
		cksum := strings.Repeat("0", 64)
		if art, err := s.Registry.Meta.Get(r.Context(), "rubygems", name, v); err == nil && len(art.Blobs) > 0 {
			cksum = art.Blobs[0].Hex()
		}
		sb.WriteString(v + " |checksum:" + cksum + "\n")
	}
	easyblobkit.Text(w, http.StatusOK, sb.String(), "text/plain")
}

func (s *State) specs(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "rubygems")
	var tuples [][2]string
	for _, n := range repos {
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", n)
		for _, v := range versions {
			if validGemVersion(v) {
				tuples = append(tuples, [2]string{n, v})
			}
		}
	}
	data := marshalSpecs(tuples)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(data)
	_ = zw.Close()
	easyblobkit.OctetResponse(w, r, gz.Bytes())
}

func validGemVersion(v string) bool {
	if v == "" {
		return false
	}
	c := v[0]
	return c >= '0' && c <= '9'
}
