package hex

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) releaseInfo(w http.ResponseWriter, r *http.Request, trimmed string) {
	rest := strings.TrimPrefix(trimmed, "packages/")
	parts := strings.Split(rest, "/")
	if len(parts) >= 3 && parts[1] == "releases" {
		name, version := parts[0], parts[2]
		if r.Method == http.MethodDelete {
			if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "hex") {
				return
			}
			if _, err := s.Registry.Meta.Get(r.Context(), "hex", name, version); err != nil {
				easyblobkit.Error(w, http.StatusNotFound, "release not found")
				return
			}
			removeVersion(s.Registry, name, version, r.Context())
			w.WriteHeader(http.StatusCreated)
			return
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"name": name, "version": version, "inserted_at": "2024-01-01T00:00:00Z"})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *State) publish(w http.ResponseWriter, r *http.Request) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "hex") {
		return
	}
	name, version := r.URL.Query().Get("name"), r.URL.Query().Get("version")
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	if tn, tv := tarballNameVersion(data); tn != "" {
		if name == "" {
			name = tn
		}
		if version == "" {
			version = tv
		}
	}
	if name == "" {
		name = "unknown"
	}
	if version == "" {
		version = "0.1.0"
	}
	filename := name + "-" + version + ".tar"
	storeVersionSource(s.Registry, name, version, filename, data, "push", r.Context())
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *State) createRelease(w http.ResponseWriter, r *http.Request, trimmed string) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "hex") {
		return
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(trimmed, "packages/"), "/releases")
	parts := strings.Split(rest, "/")
	name := parts[0]
	version := r.URL.Query().Get("version")
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	if tn, tv := tarballNameVersion(data); tn != "" {
		name = tn
		if version == "" {
			version = tv
		}
	}
	if version == "" {
		version = "0.1.0"
	}
	filename := name + "-" + version + ".tar"
	storeVersionSource(s.Registry, name, version, filename, data, "push", r.Context())
	w.WriteHeader(http.StatusCreated)
}

func removeVersion(reg *easyblobkit.Registry, name, version string, ctx context.Context) {
	if art, err := reg.Meta.Get(ctx, "hex", name, version); err == nil {
		for _, b := range art.Blobs {
			easyblobkit.LogMetaErr("blob delete", reg.Blobs.Delete(ctx, b.Digest))
		}
	}
	easyblobkit.LogMetaErr("meta delete", reg.Meta.Delete(ctx, "hex", name, version))
}
