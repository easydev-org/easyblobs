// Package npm implements the npm registry protocol: packument assembly
// (dist.tarball rewrite, shasum sha1, integrity sha512 base64), tarball
// serving, publish (raw tarball or CouchDB _attachments base64), dist-tags,
// deprecate, -rev unpublish, search, audit/profile stubs, pull-through with
// packument URL rewriting. Mirror of the pkglab reference.
package npm

import (
	"net/http"
	"net/url"
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

func init() {
	easyblobkit.Register("npm", NewHandler)
	easyblobkit.RegisterNamespace("npm", easyblobkit.ScopeNamespace)
}

// base returns the origin self-URLs are emitted on: the configured self_base,
// else a localhost fallback. Every request to this mount is served under the
// gateway's own origin, so there is no per-request mirror origin to prefer.
func (s *State) base(r *http.Request) string {
	if s.SelfBase == "" {
		return "http://localhost:8080/artifacts/npm"
	}
	return strings.TrimSuffix(s.SelfBase, "/")
}

func tarballFilename(name, version string) string {
	short := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		short = name[i+1:]
	}
	return short + "-" + version + ".tgz"
}

func unescapeName(name string) string {
	if u, err := url.QueryUnescape(name); err == nil {
		return u
	}
	return name
}

func encodeName(name string) string { return strings.ReplaceAll(name, "/", "%2F") }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/npm")
	path = strings.TrimPrefix(path, "/npm")
	path = strings.Trim(path, "/")
	method := r.Method

	switch {
	case path == "":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"db_name": "registry", "doc_count": 0})
	case path == "-/ping":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{})
	case path == "-/whoami":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"username": "anonymous"})
	case path == "-/all":
		s.allPackages(w, r)
	case path == "-/v1/login":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "token": "npm-anonymous"})
	case strings.HasPrefix(path, "-/npm/v1/security"):
		s.securityStub(w, r, path)
	case path == "-/npm/v1/user":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"name": "anonymous", "email": "", "email_verified": false, "tfa": nil})
	case strings.HasPrefix(path, "-/npm/v1/tokens"):
		s.tokens(w, r, method)
	case strings.HasPrefix(path, "-/user/"):
		s.couchUser(w, r, path, method)
	case strings.HasPrefix(path, "-/v1/search"):
		s.search(w, r)
	case strings.HasPrefix(path, "-/package/"):
		s.distTags(w, r, path, method)
	case strings.Contains(path, "/-rev/"):
		s.rev(w, r, path, method)
	case method == http.MethodPut && strings.HasSuffix(path, "/deprecate"):
		name := strings.TrimSuffix(path, "/deprecate")
		s.deprecate(w, r, name)
	case method == http.MethodPut:
		if !s.authorizePublish(w, r, unescapeName(path)) {
			return
		}
		s.publish(w, r, unescapeName(path))
	case method == http.MethodDelete:
		if !s.authorizePublish(w, r, unescapeName(path)) {
			return
		}
		s.deleteName(w, r, unescapeName(path))
	default:
		if i := strings.Index(path, "/-/"); i >= 0 {
			pkg := unescapeName(path[:i])
			file := path[i+len("/-/"):]
			if file == "package" || strings.HasPrefix(file, "package/") {
				s.distTags(w, r, path, method)
				return
			}
			if method == http.MethodGet {
				s.tarball(w, r, pkg, file)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if method == http.MethodGet {
			s.metadata(w, r, unescapeName(path))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}
