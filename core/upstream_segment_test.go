package easyblobkit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, path, nil)
}

func TestSplitUpstream(t *testing.T) {
	cases := []struct {
		in   string
		host string
		rest string
		ok   bool
	}{
		{"_upstream/registry.npmjs.org/@acme/ui", "registry.npmjs.org", "@acme/ui", true},
		{"_upstream/proxy.golang.org/example.com/m/@v/list", "proxy.golang.org", "example.com/m/@v/list", true},
		{"_upstream/registry.npmjs.org", "registry.npmjs.org", "", true},
		// SSRF guard: cluster-local / bare / IP hosts are rejected.
		{"_upstream/localhost/x", "", "_upstream/localhost/x", false},
		{"_upstream/artifact.worker.svc.cluster.local/x", "", "_upstream/artifact.worker.svc.cluster.local/x", false},
		{"_upstream/10.0.0.1/x", "", "_upstream/10.0.0.1/x", false},
		// Not the marker.
		{"@acme/ui", "", "@acme/ui", false},
		{"_upstream", "", "_upstream", false},
	}
	for _, c := range cases {
		host, rest, ok := SplitUpstream(c.in)
		if ok != c.ok || host != c.host || rest != c.rest {
			t.Errorf("SplitUpstream(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, host, rest, ok, c.host, c.rest, c.ok)
		}
	}
}

func TestSplitExplicitRepoSegment(t *testing.T) {
	cases := []struct {
		in   string
		repo string
		rest string
		ok   bool
	}{
		{"_repo/internal/simple/foo", "internal", "simple/foo", true},
		{"_repo/internal", "internal", "", true},
		{"-internal/x", "", "-internal/x", false},
		{"_repo", "", "_repo", false},
	}
	for _, c := range cases {
		repo, rest, ok := SplitExplicitRepoSegment(c.in)
		if ok != c.ok || repo != c.repo || rest != c.rest {
			t.Errorf("SplitExplicitRepoSegment(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, repo, rest, ok, c.repo, c.rest, c.ok)
		}
	}
}

// TestScopeMiddlewareUpstreamAndRepo verifies the middleware rewrites the new
// reserved segments to the adapter's native path and records the scope.
func TestScopeMiddlewareUpstreamAndRepo(t *testing.T) {
	var gotScope RepoScope
	var gotPath string
	h := ScopeMiddleware("/artifacts", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotScope = RepoScopeFrom(r.Context())
		gotPath = r.URL.Path
	}))

	// _upstream/<host>/<tail>: upstream recorded, path rewritten to native.
	h.ServeHTTP(nil, mustReq("/artifacts/npm/_upstream/registry.npmjs.org/@acme/ui"))
	if gotScope.Upstream != "registry.npmjs.org" {
		t.Fatalf("upstream = %q", gotScope.Upstream)
	}
	if gotPath != "/artifacts/npm/@acme/ui" {
		t.Fatalf("rewritten path = %q", gotPath)
	}
	if gotScope.Namespace != "@acme" || gotScope.Name != "ui" {
		t.Fatalf("scope = %+v", gotScope)
	}

	// _repo/<repo>/<tail>: explicit repository recorded, path rewritten.
	h.ServeHTTP(nil, mustReq("/artifacts/pypi/_repo/internal/simple/foo"))
	if gotScope.Namespace != "internal" || !gotScope.Explicit {
		t.Fatalf("repo scope = %+v", gotScope)
	}
	if gotPath != "/artifacts/pypi/simple/foo" {
		t.Fatalf("rewritten path = %q", gotPath)
	}
}
