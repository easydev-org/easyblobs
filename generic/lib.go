// Package generic implements the generic raw-artifact store:
// /{name}/{version}/{filename}. Local-only (no upstream) as in the reference.
package generic

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	return s, nil
}

func init() { easyblobkit.Register("generic", NewHandler) }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/generic/")
	path = strings.TrimPrefix(path, "generic/")
	path = strings.Trim(path, "/")

	if strings.HasSuffix(path, ".version") {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimSuffix(path, ".version")
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "generic") {
			return
		}
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "generic", name)
		for _, v := range vs {
			easyblobkit.LogMetaErr("meta delete", s.Registry.Meta.Delete(r.Context(), "generic", name, v))
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	parts := strings.Split(path, "/")
	if len(parts) != 3 {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	name, version, filename := parts[0], parts[1], parts[2]

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		art, err := s.Registry.Meta.Get(r.Context(), "generic", name, version)
		if err != nil {
			easyblobkit.Error(w, http.StatusNotFound, "not found")
			return
		}
		for _, b := range art.Blobs {
			if b.Name != filename {
				continue
			}
			if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", filename) {
				return
			}
		}
		easyblobkit.Error(w, http.StatusNotFound, "not found")
	case http.MethodPut:
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "generic") {
			return
		}
		easyblobkit.LimitBody(w, r)
		// StoreStream streams the body into the CAS (no in-memory copy) and
		// replaces any prior descriptor of the same filename.
		s.Registry.StoreVersion(r.Context(), easyblobkit.VersionInput{
			Format: "generic", Repository: name, Version: version,
			Filename: filename, Source: "push", Body: r.Body,
		})
		easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
	case http.MethodDelete:
		// Delete ONE file (descriptor) of a version, leaving the others intact,
		// and drop its CAS blob when nothing else references it. This is the
		// targeted delete the flat stores otherwise lack.
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "generic") {
			return
		}
		deleted, err := s.Registry.DeleteVersionFile(r.Context(), "generic", name, version, filename)
		if err != nil {
			easyblobkit.Error(w, http.StatusNotFound, "not found")
			return
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "blob_deleted": deleted})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
