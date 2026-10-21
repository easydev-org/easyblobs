package pub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	meta, err := store.OpenSQLite(dir + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	blobs, err := store.NewFileBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	reg := &easyblobkit.Registry{
		Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta),
		Upstreams: &easyblobkit.Upstreams{Defaults: map[string]string{}, Overrides: map[string]string{}, Proxy: map[string]string{}},
	}
	h, err := NewHandler(reg, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestRootIdentifiesServer: `dart pub` probes the registry root.
func TestRootIdentifiesServer(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/pub", "")
	if w.Code != http.StatusOK {
		t.Fatalf("root = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "artifact-pub") {
		t.Fatalf("root should identify the server: %s", w.Body.String())
	}
}

// TestPackagesListIsEmptyJSON: an empty registry returns a valid empty listing
// (dart pub rejects a 404 here).
func TestPackagesListIsEmptyJSON(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/pub/api/packages", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("expected an empty listing: %s", w.Body.String())
	}
}

// TestAdvisoriesIsEmpty: the client polls advisories; an empty set must be 200
// with an empty array (never 404).
func TestAdvisoriesIsEmpty(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/pub/api/packages/foo/advisories", "")
	if w.Code != http.StatusOK {
		t.Fatalf("advisories = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"advisories":[]`) {
		t.Fatalf("expected an empty advisories array: %s", w.Body.String())
	}
}

// TestUnknownPackageMetadataIs404: metadata for a package that does not exist
// must be 404, not an empty 200 (the client would then try to resolve a
// non-existent version).
func TestUnknownPackageMetadataIs404(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodGet, "/artifacts/pub/api/packages/nosuchpkg", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown package = %d, want 404 (%s)", w.Code, w.Body.String())
	}
}

// TestUnknownPathIs404
func TestUnknownPathIs404(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodGet, "/artifacts/pub/nope/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown path = %d, want 404", w.Code)
	}
}
