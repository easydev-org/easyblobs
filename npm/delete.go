package npm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) deprecate(w http.ResponseWriter, r *http.Request, name string) {
	if !s.authorizePublish(w, r, name) {
		return
	}
	name = unescapeName(name)
	easyblobkit.LimitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	var doc map[string]any
	if json.Unmarshal(body, &doc) == nil {
		if versions, ok := doc["versions"].(map[string]any); ok {
			for ver, msg := range versions {
				art, err := s.Registry.Meta.Get(r.Context(), "npm", name, ver)
				if err != nil {
					continue
				}
				pj := map[string]any{}
				_ = json.Unmarshal(art.Proprietary, &pj)
				switch v := msg.(type) {
				case string:
					if v == "" {
						delete(pj, "deprecated")
					} else {
						pj["deprecated"] = v
					}
				default:
					delete(pj, "deprecated")
				}
				art.Proprietary, _ = json.Marshal(pj)
				easyblobkit.LogMetaErr("meta put", s.Registry.Meta.Put(r.Context(), art))
			}
		}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *State) rev(w http.ResponseWriter, r *http.Request, rel, method string) {
	i := strings.Index(rel, "/-rev/")
	if i < 0 {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	pkg := unescapeName(rel[:i])
	switch method {
	case http.MethodDelete:
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "npm", pkg)
		for _, v := range vs {
			s.removeVersion(r.Context(), pkg, v)
		}
		easyblobkit.LogMetaErr("meta delete", s.Registry.Meta.Delete(r.Context(), "npm", pkg, ""))
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPut:
		easyblobkit.LimitBody(w, r)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		var doc map[string]any
		if json.Unmarshal(body, &doc) == nil {
			if name, ok := doc["name"].(string); ok {
				s.reconcileVersions(r.Context(), name, doc)
			}
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		easyblobkit.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *State) reconcileVersions(ctx context.Context, pkg string, doc map[string]any) {
	keep := map[string]bool{}
	if vs, ok := doc["versions"].(map[string]any); ok {
		for k := range vs {
			keep[k] = true
		}
	}
	current, _ := s.Registry.Meta.ListVersions(ctx, "npm", pkg)
	for _, v := range current {
		if !keep[v] {
			s.removeVersion(ctx, pkg, v)
		}
	}
}

func (s *State) deleteName(w http.ResponseWriter, r *http.Request, name string) {
	if i := strings.LastIndex(name, "/"); i > 0 {
		s.removeVersion(r.Context(), name[:i], name[i+1:])
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	vs, _ := s.Registry.Meta.ListVersions(r.Context(), "npm", name)
	for _, v := range vs {
		s.removeVersion(r.Context(), name, v)
	}
	easyblobkit.LogMetaErr("meta delete", s.Registry.Meta.Delete(r.Context(), "npm", name, ""))
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *State) removeVersion(ctx context.Context, pkg, version string) {
	if art, err := s.Registry.Meta.Get(ctx, "npm", pkg, version); err == nil {
		for _, b := range art.Blobs {
			easyblobkit.LogMetaErr("blob delete", s.Registry.Blobs.Delete(ctx, b.Digest))
		}
	}
	easyblobkit.LogMetaErr("meta delete", s.Registry.Meta.Delete(ctx, "npm", pkg, version))
}
