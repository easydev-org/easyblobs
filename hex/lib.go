// Package hex implements the Hex.pm protocol: public_key, names/versions
// (protobuf), packages metadata (protobuf), tarball download with pull-through,
// publish, release info/delete, owners, retire, and a catch-all proxy for
// bootstrap endpoints. Mirror of the pkglab reference (protobuf + gzip +
// optional Ed25519 signing).
package hex

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

func init() { easyblobkit.Register("hex", NewHandler) }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/hex")
	path = strings.TrimPrefix(path, "/hex")
	path = strings.Trim(path, "/")

	// Optional {repo}/ prefix before the standard routes. The API host
	// (hex.pm) serves everything under "api/" — strip that too, so
	// api/packages/<n>/releases/<v> and packages/<n>/releases/<v> are the
	// same routes.
	trimmed := strings.TrimPrefix(path, "hexpm/")
	trimmed = strings.TrimPrefix(trimmed, "registry/")
	trimmed = strings.TrimPrefix(trimmed, "api/")

	switch {
	case trimmed == "public_key" || strings.HasSuffix(trimmed, "/public_key"):
		s.publicKey(w, r)
	case trimmed == "names" || strings.HasSuffix(trimmed, "/names"):
		s.names(w, r)
	case trimmed == "versions" || strings.HasSuffix(trimmed, "/versions"):
		s.versions(w, r)
	case strings.HasPrefix(trimmed, "packages/") && strings.HasSuffix(trimmed, "/releases"):
		s.createRelease(w, r, trimmed)
	case strings.HasPrefix(trimmed, "packages/") && strings.Contains(trimmed, "/releases/") && strings.HasSuffix(trimmed, "/retire"):
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(trimmed, "packages/") && strings.Contains(trimmed, "/releases/"):
		s.releaseInfo(w, r, trimmed)
	case strings.HasPrefix(trimmed, "packages/") && strings.HasSuffix(trimmed, "/owners"):
		easyblobkit.JSON(w, http.StatusOK, []any{})
	case trimmed == "packages" || trimmed == "search":
		// hex.pm's package search lives on the API host (/packages?search=).
		// Merge local packages with the upstream result so both private and
		// public names are discoverable.
		s.search(w, r)
	case strings.HasPrefix(trimmed, "packages/"):
		name := strings.TrimPrefix(trimmed, "packages/")
		if r.Method == http.MethodGet {
			s.pkg(w, r, name)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	case trimmed == "publish" || strings.HasSuffix(trimmed, "/publish"):
		if r.Method == http.MethodPost {
			s.publish(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	case trimmed == "users/me":
		// `mix hex.publish` probes the authenticated user before publishing;
		// it maps the "organizations" key of this response (absent key =>
		// crash), so it must always be present.
		if wantsErlang := strings.Contains(r.Header.Get("Accept"), "hex+erlang"); wantsErlang {
			// hex_core requests the erlang external term format; JSON is also
			// accepted by hex_core only when the server rejects +erlang. Serve
			// a term_to_binary-encoded map to keep the client happy.
			term := erlangUsersMe()
			w.Header().Set("Content-Type", "application/vnd.hex+erlang")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(term)
			return
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{
			"username": "lc", "email": "lc@easy-platform.invalid",
			"inserted_at": "2024-01-01T00:00:00Z", "organizations": []any{},
		})
	case trimmed == "organizations" || strings.HasPrefix(trimmed, "organizations/"):
		// The publish task lists the user's organizations to offer
		// organization ownership; an empty list keeps the public flow.
		easyblobkit.JSON(w, http.StatusOK, []any{})
	case strings.HasPrefix(trimmed, "tarballs/"):
		s.tarball(w, r, strings.TrimPrefix(trimmed, "tarballs/"))
	default:
		s.proxyAll(w, r, path)
	}
}
