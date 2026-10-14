package huggingface

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRerootLocation locks the fix: a HOST-RELATIVE Location is re-rooted under
// the mount; an absolute URL (the LFS CDN) is left untouched.
func TestRerootLocation(t *testing.T) {
	cases := map[string]string{
		"/api/resolve-cache/models/x/y/config.json?etag=1": mountPath + "/api/resolve-cache/models/x/y/config.json?etag=1",
		"/datasets/z/resolve/main/a":                       mountPath + "/datasets/z/resolve/main/a",
		"https://us.aws.cdn.hf.co/x/y?a=b":                 "https://us.aws.cdn.hf.co/x/y?a=b",
		"//cdn.example/x":                                  "//cdn.example/x",
		"":                                                 "",
	}
	for in, want := range cases {
		if got := rerootLocation(in); got != want {
			t.Errorf("rerootLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolveHostRelativeRedirectIsRerooted drives a resolve request whose
// upstream answers a host-relative 307 (the HF small-file case) and asserts the
// client sees a Location under the mount, not at the host root.
func TestResolveHostRelativeRedirectIsRerooted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/api/resolve-cache/models/prajjwal1/bert-tiny/abc/config.json?etag=1")
		w.Header().Set("X-Repo-Commit", "deadbeef")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	s.DisableLFSCache = true // exercise the pass-through (redirect) path
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/prajjwal1/bert-tiny/resolve/main/config.json", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("code = %d, want 307", rec.Code)
	}
	want := mountPath + "/api/resolve-cache/models/prajjwal1/bert-tiny/abc/config.json?etag=1"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// The Hub headers must ride the redirect too (huggingface_hub reads
	// X-Repo-Commit to accept the endpoint).
	if got := rec.Header().Get("X-Repo-Commit"); got != "deadbeef" {
		t.Fatalf("X-Repo-Commit on redirect = %q, want deadbeef", got)
	}
}

// TestApiResolveCacheForwardsHubHeaders: the /api/resolve-cache 200 the client
// is redirected to must carry X-Repo-Commit (and ETag), or the client rejects
// it as "not a Hub endpoint".
func TestApiResolveCacheForwardsHubHeaders(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Repo-Commit", "cafef00d")
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model_type":"bert"}`))
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/api/resolve-cache/models/prajjwal1/bert-tiny/abc/config.json", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Repo-Commit"); got != "cafef00d" {
		t.Fatalf("X-Repo-Commit = %q, want cafef00d", got)
	}
	if got := rec.Header().Get("ETag"); got != `"abc"` {
		t.Fatalf("ETag = %q, want \"abc\"", got)
	}
}

// TestApiQueryStringIsForwarded: the /api tree endpoint uses ?recursive=true to
// list subdirectory entries; dropping the query returns only the top level and
// breaks snapshot_download for models with subdirectories.
func TestApiQueryStringIsForwarded(t *testing.T) {
	var gotQuery string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"file","path":"scheduler/scheduler_config.json"},{"type":"file","path":"config.json"}]`))
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/api/models/Qwen/Qwen-Image-2.1/tree/main?recursive=true", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if gotQuery != "recursive=true" {
		t.Fatalf("upstream query = %q, want recursive=true", gotQuery)
	}
	if !strings.Contains(rec.Body.String(), "scheduler/scheduler_config.json") {
		t.Fatalf("recursive listing not returned: %s", rec.Body.String())
	}
}

// TestStripXetLinks locks the Xet fallback: the rel="xet-*" Link entries (which
// point at unreachable huggingface.co / cas-server hosts) are dropped so
// huggingface_hub falls back to the classic CDN path; any other Link survives.
func TestStripXetLinks(t *testing.T) {
	in := `<https://huggingface.co/api/models/x/xet-read-token/r>; rel="xet-auth", ` +
		`<https://cas-server.xethub.hf.co/v1/reconstructions/h>; rel="xet-reconstruction-info", ` +
		`<https://example.test/other>; rel="other"`
	got := stripXetLinks(in)
	if strings.Contains(got, "xet-") {
		t.Fatalf("xet links not stripped: %q", got)
	}
	if !strings.Contains(got, `rel="other"`) {
		t.Fatalf("non-xet link dropped: %q", got)
	}
	if stripXetLinks("") != "" {
		t.Fatal("empty link must stay empty")
	}
	// A Link with no xet entry is returned verbatim.
	plain := `<https://example.test/a>; rel="b"`
	if stripXetLinks(plain) != plain {
		t.Fatalf("plain link altered: %q", stripXetLinks(plain))
	}
}

// TestForwardHubHeadersDropsXetLink: with LFS caching DISABLED the resolve 302
// is passed through; the forwarded response must not advertise xet Links and
// must still carry X-Repo-Commit.
func TestForwardHubHeadersDropsXetLink(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://us.aws.cdn.hf.co/blob?sig=x")
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("Link", `<https://huggingface.co/api/models/x/xet-read-token/r>; rel="xet-auth"`)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	s.DisableLFSCache = true // exercise the pass-through path
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.bin", nil))
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("code = %d, want 307", rec.Code)
	}
	if l := rec.Header().Get("Link"); strings.Contains(l, "xet-") {
		t.Fatalf("xet Link leaked downstream: %q", l)
	}
	if rec.Header().Get("X-Repo-Commit") != "rev1" {
		t.Fatal("X-Repo-Commit must still be forwarded")
	}
}

// TestLFSIsCachedServerSide: with caching enabled (default), a resolve 307 is
// NOT passed through — artifact fetches the CDN bytes into the CAS and serves
// them itself, so a repeat pull hits local storage.
func TestLFSIsCachedServerSide(t *testing.T) {
	blob := []byte("WEIGHTS-0123456789")
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(blob)
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
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.bin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (cached serve), body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), blob) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), blob)
	}
	if rec.Header().Get("X-Repo-Commit") != "rev1" {
		t.Fatal("X-Repo-Commit must be replayed on the cached serve")
	}

	// A second request must be served from the CAS (the CDN is closed).
	cdn.Close()
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.bin", nil))
	if rec2.Code != http.StatusOK || !bytes.Equal(rec2.Body.Bytes(), blob) {
		t.Fatalf("second (cached) serve: code=%d body=%q", rec2.Code, rec2.Body.Bytes())
	}
}

// TestLFSHeadDoesNotDownload: a HEAD for an uncached LFS file must answer
// immediately with metadata, never fetching the (multi-GB) CDN object.
func TestLFSHeadDoesNotDownload(t *testing.T) {
	var cdnHits int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cdnHits, 1) // the body must never be fetched for a HEAD
		_, _ = w.Write([]byte("WEIGHTS"))
	}))
	t.Cleanup(cdn.Close)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", cdn.URL+"/blob")
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("X-Linked-Size", "123456789")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(hub.Close)

	s := newFixture(t, hub.URL, "")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/artifacts/huggingface/x/resolve/main/w.bin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD code = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != "123456789" {
		t.Fatalf("Content-Length = %q, want 123456789", got)
	}
	if rec.Header().Get("X-Repo-Commit") != "rev1" {
		t.Fatal("HEAD must carry X-Repo-Commit")
	}
	if n := atomic.LoadInt32(&cdnHits); n != 0 {
		t.Fatalf("HEAD fetched the CDN body %d time(s) — must not download", n)
	}
}
