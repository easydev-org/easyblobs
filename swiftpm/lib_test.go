package swift

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

// TestOptionsAdvertisesServiceDoc pins the SwiftPM registry handshake: the client
// issues OPTIONS and REQUIRES the Content-Version and Link service-doc headers,
// otherwise it refuses to talk to the registry at all.
func TestOptionsAdvertisesServiceDoc(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodOptions, "/artifacts/swift/some/path", "")
	if w.Code != http.StatusOK {
		t.Fatalf("options = %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Version") == "" {
		t.Fatal("OPTIONS must carry Content-Version")
	}
	link := w.Header().Get("Link")
	if !strings.Contains(link, "service-doc") {
		t.Fatalf("OPTIONS must carry a Link service-doc header, got %q", link)
	}
	if allow := w.Header().Get("Allow"); allow == "" {
		t.Fatal("OPTIONS must carry Allow")
	}
}

// TestIdentifiersIsJSON: the registry has to answer the identifiers endpoint
// with JSON (the client parses it), even when empty.
func TestIdentifiersIsJSON(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/swift/identifiers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("identifiers = %d %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "json") {
		t.Fatalf("identifiers Content-Type = %q, want json", ct)
	}
}

// TestDeleteNeedsVersionPath: DELETE on a path that is not {scope}/{name}/{version}
// must be rejected (the spec only defines release removal).
func TestDeleteNeedsVersionPath(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodDelete, "/artifacts/swift/only/one", ""); w.Code != http.StatusNotFound {
		t.Fatalf("short delete = %d, want 404", w.Code)
	}
}
