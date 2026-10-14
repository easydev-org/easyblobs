package huggingface

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLFSRangeIsProxiedNotCached: an uncached Range request (a resume) must be
// proxied to the CDN and answered 206 IMMEDIATELY, not via "cache the whole
// file first" (which makes the client time out and restart from zero).
func TestLFSRangeIsProxiedNotCached(t *testing.T) {
	full := bytes.Repeat([]byte("0123456789"), 1000) // 10000 bytes
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=5000-" {
			t.Errorf("Range not forwarded to CDN: %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 5000-9999/10000")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(full[5000:])
	}))
	t.Cleanup(cdn.Close)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", cdn.URL+"/blob")
		w.Header().Set("X-Repo-Commit", "rev1")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hub.Close)

	s := newFixture(t, hub.URL, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.bin", nil)
	req.Header.Set("Range", "bytes=5000-")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("code = %d, want 206", rec.Code)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 5000-9999/10000" {
		t.Fatalf("Content-Range = %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), full[5000:]) {
		t.Fatalf("range body mismatch: %d bytes", rec.Body.Len())
	}
}
