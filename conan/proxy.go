package conan

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) replyOrProxy(w http.ResponseWriter, r *http.Request, local any, upstreamPath string) {
	if local != nil {
		easyblobkit.JSON(w, http.StatusOK, local)
		return
	}
	// The center sub-endpoint lives on center2.conan.io; host-driven
	// resolution picks whichever the client actually dialed.
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "conan", Sub: "center"}); err == nil {
		if body, err := remote.GetBytes(r.Context(), upstreamPath); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		} else if st, ok := err.(*easyblobkit.UpstreamStatusError); ok && st.Status == http.StatusNotFound {
			// Propagate a genuine "not in the upstream" as 404: the conan
			// client uses it to mean "no prebuilt binary — build locally",
			// and treats a gateway error as fatal instead.
			easyblobkit.Error(w, http.StatusNotFound, "not found")
			return
		}
	}
	easyblobkit.Error(w, http.StatusBadGateway, "upstream")
}

func (s *State) proxyRecipe(w http.ResponseWriter, r *http.Request, path string) {
	// Catch-all GET proxy for recipe metadata.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "conan", Sub: "center"})
	if err != nil {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	if body, err := remote.GetBytes(r.Context(), "/v2/"+strings.TrimLeft(path, "/")); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	} else {
		easyblobkit.WriteUpstreamError(w, err)
		return
	}
}

func (s *State) base() string { return easyblobkit.SelfURL(s.SelfBase, "conan") }

// fetchUpstreamFile pulls a conans/... path's bytes through from center2 so
// package/recipe files are cached server-side on first access.
func (s *State) fetchUpstreamFile(r *http.Request, path string) ([]byte, bool) {
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "conan", Sub: "center"})
	if err != nil {
		return nil, false
	}
	body, err := remote.GetBytes(r.Context(), "/v2/"+strings.TrimLeft(path, "/"))
	if err != nil {
		return nil, false
	}
	return body, true
}
