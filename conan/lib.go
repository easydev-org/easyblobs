// Package conan implements the Conan 2.x repository protocol: ping, auth,
// search, recipe/package revision metadata, file get/put, upload_urls,
// download via pull-through proxy. Mirror of the pkglab reference (subset of
// the 15 protocol routes necessary for Conan 2.x clients).
package conan

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	SelfBase string
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["self_base"].(string); ok {
		s.SelfBase = v
	}
	return s, nil
}

func init() { easyblobkit.Register("conan", NewHandler) }

const capabilities = "json,rev2,revisions,checksums,upload_zip"

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/conan")
	path = strings.TrimPrefix(path, "/conan")
	path = strings.Trim(path, "/")

	// Strip the {version} prefix segment (v2).
	if i := strings.Index(path, "/"); i > 0 {
		path = path[i+1:]
	}

	switch {
	case path == "ping":
		w.Header().Set("X-Conan-Server-Capabilities", capabilities)
		w.WriteHeader(http.StatusOK)
	case path == "users/authenticate":
		s.authenticate(w, r)
	case path == "users/check_credentials":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	case strings.HasPrefix(path, "conans/"):
		s.conans(w, r, path)
	case strings.HasPrefix(path, "files/"):
		s.files(w, r, path)
	default:
		s.proxyRecipe(w, r, path)
	}
}

func (s *State) authenticate(w http.ResponseWriter, r *http.Request) {
	if s.Auth != nil {
		if u := s.Auth.Authenticate(r.Context(), r); u != "" {
			tok := s.Auth.IssueToken(r.Context(), u, nil, 3600)
			easyblobkit.Text(w, http.StatusOK, tok, "text/plain")
			return
		}
	}
	easyblobkit.Text(w, http.StatusOK, "anonymous-token", "text/plain")
}
