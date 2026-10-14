package npm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) distTags(w http.ResponseWriter, r *http.Request, rel, method string) {
	rest := strings.TrimSuffix(rel, "/")
	if !strings.HasPrefix(rest, "-/package/") {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	rest = strings.TrimPrefix(rest, "-/package/")
	idx := strings.Index(rest, "/dist-tags")
	if idx < 0 {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	pkg := unescapeName(rest[:idx])
	tag := strings.Trim(strings.TrimPrefix(rest[idx+len("/dist-tags"):], "/"), "/")

	switch method {
	case http.MethodGet:
		dt := s.distTagsMap(r.Context(), pkg)
		if tag != "" {
			if v, ok := dt[tag]; ok {
				easyblobkit.JSON(w, http.StatusOK, v)
				return
			}
			easyblobkit.Error(w, http.StatusNotFound, "not found")
			return
		}
		easyblobkit.JSON(w, http.StatusOK, dt)
	case http.MethodPut:
		if !s.authorizePublish(w, r, pkg) {
			return
		}
		easyblobkit.LimitBody(w, r)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		version := strings.Trim(strings.TrimSpace(string(body)), `"`)
		if version == "" {
			easyblobkit.Error(w, http.StatusNotFound, "missing version")
			return
		}
		dt := s.distTagsMap(r.Context(), pkg)
		if _, err := s.Registry.Meta.Get(r.Context(), "npm", pkg, version); err != nil {
			easyblobkit.Error(w, http.StatusNotFound, "version "+version+" not found")
			return
		}
		dt[tag] = version
		s.saveDistTags(r.Context(), pkg, dt)
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		if !s.authorizePublish(w, r, pkg) {
			return
		}
		dt := s.distTagsMap(r.Context(), pkg)
		delete(dt, tag)
		s.saveDistTags(r.Context(), pkg, dt)
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *State) distTagsMap(ctx context.Context, pkg string) map[string]any {
	m := map[string]any{}
	versions, _ := s.Registry.Meta.ListVersions(ctx, "npm", pkg)
	latest := easyblobkit.HighestVersion(versions)
	if latest == "" && len(versions) > 0 {
		latest = versions[len(versions)-1]
	}
	if latest != "" {
		m["latest"] = latest
	}
	if art, err := s.Registry.Meta.Get(ctx, "npm", pkg, ""); err == nil && len(art.Proprietary) > 0 {
		var root map[string]any
		if json.Unmarshal(art.Proprietary, &root) == nil {
			if dt, ok := root["dist-tags"].(map[string]any); ok && len(dt) > 0 {
				return dt
			}
		}
	}
	return m
}

func (s *State) saveDistTags(ctx context.Context, pkg string, dt map[string]any) {
	root := map[string]any{"dist-tags": dt}
	b, _ := json.Marshal(root)
	easyblobkit.LogMetaErr("meta put", s.Registry.Meta.Put(ctx, easyblobkit.Artifact{Format: "npm", Repository: pkg, Version: "", Proprietary: b}))
}
