package huggingface

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// TestEmptyUpstreamBodyIsNotCached: an upstream 200 with a 0-byte body must NOT
// be cached (that is how a "poisoned" empty entry would replay forever); the
// client still gets the (empty) body, but the next request re-fetches.
func TestEmptyUpstreamBodyIsNotCached(t *testing.T) {
	var calls int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // empty body
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: code=%d", i, rec.Code)
		}
	}
	if calls < 2 {
		t.Fatalf("empty body was cached (upstream called %d time(s), want 2)", calls)
	}
}

// TestPoisonedZeroByteBlobSelfHeals: a cache row pointing at a 0-byte blob must
// be treated as a miss, so the file is re-fetched from upstream.
func TestPoisonedZeroByteBlobSelfHeals(t *testing.T) {
	good := []byte(`{"ok":true}`)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Repo-Commit", "rev1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(good)
	}))
	t.Cleanup(up.Close)

	s := newFixture(t, up.URL, "")
	// Seed a poisoned 0-byte blob + a row that points at it.
	stored, _, err := s.Registry.Blobs.Put(t.Context(), bytes.NewReader(nil), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Registry.Meta.Put(t.Context(), easyblobkit.Artifact{
		Format: "huggingface", Repository: "models/x", Version: "x/resolve/main/w.json",
		Digest: stored.Digest,
		Blobs:  []easyblobkit.Descriptor{{Digest: stored.Digest, Size: 0, Name: "w.json"}},
		Source: "pull",
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/huggingface/x/resolve/main/w.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), good) {
		t.Fatalf("poisoned 0-byte entry not healed: got %q", rec.Body.Bytes())
	}
}
