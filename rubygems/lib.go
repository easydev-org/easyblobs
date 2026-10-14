// Package rubygems implements the RubyGems protocol: compact index,
// specs.4.8.gz (Ruby Marshal), gem download with pull-through, upload, yank,
// search, owners, api_key stubs, dependencies. Mirror of the pkglab reference
// (includes the byte-exact Ruby Marshal 4.8 encoders).
package rubygems

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

func init() { easyblobkit.Register("rubygems", NewHandler) }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/rubygems")
	path = strings.TrimPrefix(path, "/rubygems")
	path = strings.Trim(path, "/")

	switch {
	case path == "specs.4.8.gz" || path == "latest_specs.4.8.gz" || path == "prerelease_specs.4.8.gz":
		s.specs(w, r)
	case path == "names":
		s.names(w, r)
	case path == "versions":
		s.compactVersions(w, r)
	case strings.HasPrefix(path, "info/"):
		s.compactInfo(w, r, strings.TrimPrefix(path, "info/"))
	case strings.HasPrefix(path, "api/v1/versions/"):
		rest := strings.TrimPrefix(path, "api/v1/versions/")
		if strings.HasSuffix(rest, "/latest") || strings.HasSuffix(rest, "/latest.json") {
			s.latest(w, r, strings.TrimSuffix(strings.TrimSuffix(rest, "/latest"), ".json"))
			return
		}
		s.versionsAPI(w, r, strings.TrimSuffix(rest, ".json"))
	case strings.HasPrefix(path, "api/v1/gems") && strings.HasSuffix(path, "/owners"):
		s.owners(w, r)
	case path == "api/v1/gems/yank":
		// RubyGems `gem yank` sends DELETE .../yank with gem_name + version
		// in the url-encoded request BODY (not the query string). Read both.
		// NOTE: must precede the generic ".../gems/{name}/yank" prefix case,
		// which would otherwise match this exact path.
		s.yankFromRequest(w, r)
	case strings.HasPrefix(path, "api/v1/gems/") && strings.HasSuffix(path, "/yank"):
		if r.Method == http.MethodDelete {
			name := strings.TrimSuffix(strings.TrimPrefix(path, "api/v1/gems/"), "/yank")
			s.yank(w, r, name, r.URL.Query().Get("version"))
		}
	case strings.HasPrefix(path, "api/v2/rubygems/"):
		s.versionV2(w, r, path)
	case path == "api/v1/gems":
		if r.Method == http.MethodPost {
			s.upload(w, r)
			return
		}
		s.gemsList(w, r)
	case strings.HasPrefix(path, "api/v1/gems/"):
		s.gemsInfo(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "api/v1/gems/"), ".json"))
	case path == "api/v1/dependencies":
		s.dependencies(w, r)
	case path == "api/v1/search" || path == "api/v1/search.json" || path == "api/v1/search.yaml":
		s.searchGem(w, r)
	case path == "api/v1/api_key":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"rubygems_api_key": "test-token", "name": "test"})
	case strings.HasPrefix(path, "gems/"):
		s.download(w, r, strings.TrimPrefix(path, "gems/"))
	case strings.HasPrefix(path, "quick/"):
		s.quickMarshal(w, r, strings.TrimPrefix(path, "quick/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
