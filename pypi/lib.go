// Package pypi implements the PyPI protocol (PEP 503 / 691 / 658) with
// pull-through and href rewriting, mirroring the pkglab reference.
package pypi

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

func init() { easyblobkit.Register("pypi", NewHandler) }

// NormalizeName follows PEP 503: lowercase and collapse runs of -_. to a
// single hyphen.
func NormalizeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastSep := false
	for _, c := range name {
		if c == '-' || c == '_' || c == '.' {
			if !lastSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			lastSep = true
		} else {
			b.WriteRune(c)
			lastSep = false
		}
	}
	return strings.Trim(b.String(), "-")
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/vnd.pypi.simple.v1+json")
}

func (s *State) base() string { return easyblobkit.SelfURL(s.SelfBase, "pypi") }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/pypi")
	path = strings.TrimPrefix(path, "/pypi")

	switch {
	case r.URL.Path == "/upload" || path == "/upload" || r.URL.Path == "/":
		if r.Method == http.MethodPost {
			s.upload(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case strings.HasPrefix(path, "/simple/") || strings.HasSuffix(path, "/simple"):
		s.simple(w, r, path)
		return
	case strings.HasPrefix(path, "/api/projects/"):
		s.projectAPI(w, r, path)
		return
	default:
		easyblobkit.Error(w, http.StatusNotFound, "not found")
	}
}
