package pypi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) upload(w http.ResponseWriter, r *http.Request) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "pypi") {
		return
	}
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	ct := r.Header.Get("Content-Type")
	var name, version, filename string
	if strings.HasPrefix(ct, "multipart/form-data") {
		name, _ = easyblobkit.ExtractTextField(data, ct, "name")
		version, _ = easyblobkit.ExtractTextField(data, ct, "version")
		filename, data, _ = easyblobkit.ExtractFirstFile(data, ct)
	}
	if name == "" {
		// JSON fallback.
		name = jsonField(data, "name")
		version = jsonField(data, "version")
		if filename == "" {
			filename = jsonField(data, "filename")
		}
	}
	if name == "" {
		easyblobkit.Error(w, http.StatusBadRequest, "missing name")
		return
	}
	if filename == "" {
		filename = "unknown.bin"
	}
	s.storeVersion(name, version, filename, data, "push", r.Context())
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *State) projectAPI(w http.ResponseWriter, r *http.Request, path string) {
	// /api/projects/{name}
	name := NormalizeName(strings.Trim(strings.TrimPrefix(path, "/api/projects/"), "/"))
	if r.Method == http.MethodDelete {
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "pypi") {
			return
		}
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "pypi", name)
		for _, v := range vs {
			removeVersion(s.Registry, name, v, r.Context())
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *State) storeVersion(name, version, filename string, data []byte, source string, ctx context.Context) {
	name = NormalizeName(name)
	if version == "" {
		version = "0.1.0"
	}
	s.Registry.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "pypi", Repository: name, Version: version,
		Filename: filename, Source: source, Data: data,
		Proprietary: []byte(`{"upload_time":"2024-01-01T00:00:00.000000Z"}`),
	})
}

func removeVersion(reg *easyblobkit.Registry, name, version string, ctx context.Context) {
	if art, err := reg.Meta.Get(ctx, "pypi", name, version); err == nil {
		for _, b := range art.Blobs {
			easyblobkit.LogMetaErr("blob delete", reg.Blobs.Delete(ctx, b.Digest))
		}
	}
	easyblobkit.LogMetaErr("meta delete", reg.Meta.Delete(ctx, "pypi", name, version))
}

func sharedCache() *easyblobkit.IndexCache {
	return easyblobkit.SharedIndexCache()
}

func jsonField(data []byte, key string) string {
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
