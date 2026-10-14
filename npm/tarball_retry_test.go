package npm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// TestTarballTransientUpstreamIs503Not404 locks the fix: a tarball fetch that
// keeps failing transiently (502) must surface as 503 + Retry-After — NOT a
// hard 404 — so npm retries instead of aborting `npm ci`.
func TestTarballTransientUpstreamIs503Not404(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // always transient
	}))
	t.Cleanup(up.Close)

	s := newState(t)
	s.Registry.Upstreams.Defaults["npm"] = up.URL
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/left-pad/-/left-pad-1.3.0.tgz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After on transient 503")
	}
}

// TestTarballDefinitiveMissingIs404: a tarball absent from both the conventional
// path and the packument is a definitive 404 (no retry storm, no 503).
func TestTarballDefinitiveMissingIs404(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(up.Close)

	s := newState(t)
	s.Registry.Upstreams.Defaults["npm"] = up.URL
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/left-pad/-/left-pad-1.3.0.tgz", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

// TestTarballRetriesTransientThenSucceeds: the upstream 502s twice then serves
// the tarball — the adapter's bounded retry must deliver a 200 (this is the
// observed "random 404, retry succeeds" pattern, fixed at the source).
func TestTarballRetriesTransientThenSucceeds(t *testing.T) {
	tb := tarball(t, "left-pad", "1.3.0")
	var n int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(tb)
	}))
	t.Cleanup(up.Close)

	s := newState(t)
	s.Registry.Upstreams.Defaults["npm"] = up.URL
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/left-pad/-/left-pad-1.3.0.tgz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 after retry (body=%s)", rec.Code, rec.Body.String())
	}
}

// ensure the easyblobkit import is used (TransientUpstream is exercised via the
// handler above; this keeps the import explicit).
var _ = easyblobkit.TransientUpstream
