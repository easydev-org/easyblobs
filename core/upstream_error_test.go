package easyblobkit

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteUpstreamError locks the shared mapping: a transient upstream error is
// a retryable 503 (not a hard 404); a definitive one is 404.
func TestWriteUpstreamError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"502", &UpstreamStatusError{Path: "/x", Status: 502}, http.StatusServiceUnavailable},
		{"transport", errors.New("dial tcp: refused"), http.StatusServiceUnavailable},
		{"404", &UpstreamStatusError{Path: "/x", Status: 404}, http.StatusNotFound},
		{"403", &UpstreamStatusError{Path: "/x", Status: 403}, http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		WriteUpstreamError(rec, c.err)
		if rec.Code != c.want {
			t.Errorf("%s: code=%d want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: missing Retry-After", c.name)
		}
	}
}

// TestWriteCacheResultError locks the PathCacheResult mapping (Transient → 503).
func TestWriteCacheResultError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteCacheResultError(rec, PathCacheResult{OK: false, Transient: true})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("transient: code=%d retry-after=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	rec = httptest.NewRecorder()
	WriteCacheResultError(rec, PathCacheResult{OK: false})
	if rec.Code != http.StatusNotFound {
		t.Errorf("definitive: code=%d want 404", rec.Code)
	}
}
