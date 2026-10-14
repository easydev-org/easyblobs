package pypi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) simple(w http.ResponseWriter, r *http.Request, path string) {
	rel := strings.Trim(strings.TrimPrefix(path, "/simple"), "/")
	if rel == "" {
		s.simpleRoot(w, r)
		return
	}
	parts := strings.SplitN(rel, "/", 2)
	project := NormalizeName(parts[0])
	if len(parts) == 1 {
		s.simpleProject(w, r, project)
		return
	}
	fname := parts[1]
	if strings.HasSuffix(fname, ".metadata") {
		s.metadataFile(w, r, project, strings.TrimSuffix(fname, ".metadata"))
		return
	}
	s.simpleFile(w, r, project, fname)
}

func (s *State) simpleRoot(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "pypi")
	// The root index lists only local projects. The upstream root is ~46MB
	// (tens of thousands of names), so it is never merged here; per-project
	// pages (/simple/<name>/) merge upstream, which is the path installers use.
	if wantsJSON(r) {
		projects := make([]any, 0, len(repos))
		for _, n := range repos {
			projects = append(projects, map[string]any{"name": n})
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"meta": map[string]any{"api-version": "1.4"}, "projects": projects})
		return
	}
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><body>\n")
	for _, name := range repos {
		sb.WriteString(`<a href="` + s.base() + "/simple/" + easyblobkit.URLencode(name) + "/\">" + name + "</a>\n")
	}
	sb.WriteString("</body></html>")
	easyblobkit.Text(w, http.StatusOK, sb.String(), "text/html")
}

func (s *State) simpleProject(w http.ResponseWriter, r *http.Request, name string) {
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "pypi", name)
	// Always merge upstream files so a local partial cache (or a single
	// pulled wheel) does not shadow the rest of the project. A pull-through
	// mirror must present the full index; the reference only proxies on a
	// total miss, but uv/pip resolve ranges against the whole page.
	if s.mergeSimpleProject(w, r, name, versions) {
		return
	}
	if wantsJSON(r) {
		s.simpleProjectJSON(w, r, name, versions)
		return
	}
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><body>\n")
	for _, v := range versions {
		art, err := s.Registry.Meta.Get(r.Context(), "pypi", name, v)
		if err != nil {
			continue
		}
		for _, b := range art.Blobs {
			if b.Name == "" {
				continue
			}
			sb.WriteString(`<a href="` + s.base() + "/simple/" + easyblobkit.URLencode(name) + "/" + easyblobkit.URLencode(b.Name) + "#sha256=" + b.Hex() + `">` + b.Name + "</a>\n")
		}
	}
	sb.WriteString("</body></html>")
	easyblobkit.Text(w, http.StatusOK, sb.String(), "text/html")
}

func (s *State) simpleProjectJSON(w http.ResponseWriter, r *http.Request, name string, versions []string) {
	var files []any
	for _, v := range versions {
		art, err := s.Registry.Meta.Get(r.Context(), "pypi", name, v)
		if err != nil {
			continue
		}
		for _, b := range art.Blobs {
			if b.Name == "" {
				continue
			}
			entry := map[string]any{
				"filename": b.Name,
				"url":      s.base() + "/simple/" + easyblobkit.URLencode(name) + "/" + easyblobkit.URLencode(b.Name),
				"hashes":   map[string]any{"sha256": b.Hex()},
			}
			if b.Size > 0 {
				entry["size"] = b.Size
			}
			entry["upload-time"] = "2024-01-01T00:00:00Z"
			files = append(files, entry)
		}
	}
	out, _ := json.Marshal(map[string]any{
		"meta": map[string]any{"api-version": "1.4"}, "name": name, "versions": versions, "files": files,
	})
	easyblobkit.Text(w, http.StatusOK, string(out), "application/vnd.pypi.simple.v1+json")
}
