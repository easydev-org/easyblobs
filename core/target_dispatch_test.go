package easyblobkit

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/easydev-org/easyblobs/targets"
)

// recordingHandler captures the path and scope the adapter would see.
type recordingHandler struct {
	path  string
	scope RepoScope
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.path = r.URL.Path
	h.scope = RepoScopeFrom(r.Context())
	w.WriteHeader(http.StatusOK)
}

func newDispatch(t *testing.T) (*TargetDispatcher, *recordingHandler, *targets.Registry) {
	t.Helper()
	reg := targets.NewRegistry()
	r := &Registry{Upstreams: &Upstreams{Targets: reg, Defaults: reg.LegacyDefaults()}}
	h := &recordingHandler{}
	d := NewTargetDispatcher(MountBase, r, map[string]http.Handler{"maven": h, "npm": h})
	return d, h, reg
}

// TestNamedTargetNotMounted checks a NAMED target is no longer a mount:
// /artifacts/maven.google/... does not resolve (there is no "maven.google"
// adapter), so the mirror is reached via /artifacts/maven/_upstream/<host>/...
// instead.
func TestNamedTargetNotMounted(t *testing.T) {
	d, _, _ := newDispatch(t)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/maven.google/org/slf4j/x.pom", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("named-target mount = %d, want 404", rec.Code)
	}
}

// TestDispatchUpstreamSelectsNamedMirror checks the reserved-segment form
// reaches a named mirror: /artifacts/maven/_upstream/dl.google.com/... records
// the explicit upstream and rewrites to the maven mount.
func TestDispatchUpstreamSelectsNamedMirror(t *testing.T) {
	d, h, _ := newDispatch(t)
	req := httptest.NewRequest(http.MethodGet, "/artifacts/maven/_upstream/dl.google.com/org/slf4j/x.pom", nil)
	d.ServeHTTP(httptest.NewRecorder(), req)
	if h.path != "/artifacts/maven/org/slf4j/x.pom" {
		t.Errorf("path = %q", h.path)
	}
	if h.scope.Upstream != "dl.google.com" {
		t.Errorf("upstream = %q", h.scope.Upstream)
	}
}

// TestDispatchDefaultProtocolIsTransparent checks /artifacts/<protocol>/ stays
// unchanged.
func TestDispatchDefaultProtocolIsTransparent(t *testing.T) {
	d, h, _ := newDispatch(t)
	req := httptest.NewRequest(http.MethodGet, "/artifacts/npm/left-pad", nil)
	d.ServeHTTP(httptest.NewRecorder(), req)
	if h.path != "/artifacts/npm/left-pad" {
		t.Errorf("path = %q", h.path)
	}
	if h.scope.Format != "npm" || h.scope.Name != "left-pad" {
		t.Errorf("scope = %+v", h.scope)
	}
}

// TestDispatchUserNamedTargetNotMounted checks a user-created named target is
// also not a mount (reached via _upstream instead).
func TestDispatchUserNamedTargetNotMounted(t *testing.T) {
	d, _, reg := newDispatch(t)
	reg.Put(targets.Target{ID: "maven.corp", Protocol: "maven", Base: "https://nexus.corp/m2"})
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/maven.corp/org/acme/lib/1.0/lib-1.0.pom", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("user named-target mount = %d, want 404", rec.Code)
	}
}

// TestDispatchUnknownTargetNotFound checks an unknown segment 404s.
func TestDispatchUnknownTargetNotFound(t *testing.T) {
	d, _, _ := newDispatch(t)
	req := httptest.NewRequest(http.MethodGet, "/artifacts/nope/x", nil)
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
}
