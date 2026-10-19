// Package generic implements the generic raw-artifact store:
// /{name}/{version}/{filename}. Local-only (no upstream) as in the reference.
package generic

import (
	"io"
	"net/http"
	"strings"
	"time"

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

// pullThrough fetches a generic artifact from the configured upstream (when
// one is set) and caches it, recording the upstream Last-Modified as the
// publish time. Returns true when it served the artifact.
func (s *State) pullThrough(w http.ResponseWriter, r *http.Request, name, version, filename string) bool {
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "generic"})
	if err != nil {
		return false
	}
	path := "/" + name + "/" + version + "/" + filename
	resp, err := remote.Get(r.Context(), path)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return false
	}
	fetched, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	var publishedAt time.Time
	if t, perr := http.ParseTime(resp.Header.Get("Last-Modified")); perr == nil {
		publishedAt = t
	}
	s.Registry.StoreVersion(r.Context(), easyblobkit.VersionInput{
		Format: "generic", Repository: name, Version: version,
		Filename: filename, Source: "pull", Data: fetched, PublishedAt: publishedAt,
	})
	easyblobkit.ServeData(w, r, s.Registry, r.Context(), fetched, "application/octet-stream", filename)
	return true
}

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
		if err == nil {
			for _, b := range art.Blobs {
				if b.Name != filename {
					continue
				}
				if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", filename) {
					return
				}
			}
		}
		// Pull-through: fetch <name>/<version>/<filename> from the configured
		// upstream, cache it (recording the upstream publish time), and serve.
		if !s.pullThrough(w, r, name, version, filename) {
			easyblobkit.Error(w, http.StatusNotFound, "not found")
		}
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
