package huggingface

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHubRelativeRedirectEndToEnd drives the real client flow: the Hub answers
// the resolve with a host-relative 307, the adapter re-roots it under the mount,
// and following it (the /api/resolve-cache path) returns the file BYTES — never
// a 200 with an empty body (the #57/#62 regression).
func TestHubRelativeRedirectEndToEnd(t *testing.T) {
	body := []byte(`{"model_type":"bert"}`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/resolve/"):
			w.Header().Set("X-Repo-Commit", "rev1")
			w.Header().Set("Location", "/api/resolve-cache/models/x/y/rev/config.json?etag=1")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case strings.HasPrefix(r.URL.Path, "/api/resolve-cache/"):
			w.Header().Set("X-Repo-Commit", "rev1")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	// 1) resolve → 307 (passed through, re-rooted under the mount).
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/config.json", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("resolve code = %d, want 307", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, mountPath) {
		t.Fatalf("Location not re-rooted: %q", loc)
	}
	// 2) follow the re-rooted Location → the real bytes (not 200/empty).
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, loc, nil))
	if rec2.Code != http.StatusOK || !bytes.Equal(rec2.Body.Bytes(), body) {
		t.Fatalf("follow: code=%d body=%q want %q", rec2.Code, rec2.Body.Bytes(), body)
	}
}

// TestHubRelativeRedirectPassedThroughNotFetchedAsCDN reproduces the #62 bug:
// a SMALL (non-LFS) file is answered by the Hub with a HOST-RELATIVE 307 to
// "/api/resolve-cache/...". With LFS caching ENABLED (the default), the old code
// handed that bare relative path to the CDN fetcher, which failed with
// `unsupported protocol scheme ""` AFTER a 200 was already committed — serving
// an EMPTY body. The fix must PASS THE REDIRECT THROUGH (re-rooted under the
// mount), not fetch it server-side.
func TestHubRelativeRedirectPassedThroughNotFetchedAsCDN(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Hub's small-file response: a host-relative 307.
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("Location", "/api/resolve-cache/models/x/y/rev/config.json?etag=1")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "") // LFS caching stays ENABLED
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/config.json", nil))

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("code = %d, want 307 (pass-through); body=%q", rec.Code, rec.Body.String())
	}
	want := mountPath + "/api/resolve-cache/models/x/y/rev/config.json?etag=1"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// The Hub headers must ride the redirect (huggingface_hub requires it).
	if got := rec.Header().Get("X-Repo-Commit"); got != "rev1" {
		t.Fatalf("X-Repo-Commit = %q, want rev1", got)
	}
}

// TestCrossHostCDNRedirectStillCached locks the complement: an ABSOLUTE redirect
// to a DIFFERENT host (the LFS CDN) is still fetched into the CAS and served
// as 200 with the real bytes.
func TestCrossHostCDNRedirectStillCached(t *testing.T) {
	blob := []byte("WEIGHTS-CDN-BYTES")
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(blob)
	}))
	t.Cleanup(cdn.Close)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("Location", cdn.URL+"/blob")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hub.Close)

	s := newFixture(t, hub.URL, "")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.bin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (cached serve); body=%q", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), blob) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), blob)
	}
}
