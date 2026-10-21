package generic

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// newHandler builds the adapter over a throwaway sqlite metadata DB and a
// filesystem CAS. auth is nil => open (dev) instance, so writes are permitted —
// which is what lets the tests exercise the full PUT/GET round trip.
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

// TestPutGetDeleteRoundTrip pins the core generic contract: a raw artifact is
// stored under {name}/{version}/{filename}, served back byte-for-byte, and
// deleted one file at a time.
func TestPutGetDeleteRoundTrip(t *testing.T) {
	h := newHandler(t)
	const p = "/artifacts/generic/tool/1.0.0/tool.tar.gz"

	if w := do(h, http.MethodPut, p, "RAW-BYTES"); w.Code != http.StatusCreated {
		t.Fatalf("put = %d %s", w.Code, w.Body.String())
	}
	w := do(h, http.MethodGet, p, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d %s", w.Code, w.Body.String())
	}
	got, _ := io.ReadAll(w.Body)
	if string(got) != "RAW-BYTES" {
		t.Fatalf("bytes = %q", string(got))
	}
	// HEAD is supported and carries no body.
	if w := do(h, http.MethodHead, p, ""); w.Code != http.StatusOK {
		t.Fatalf("head = %d", w.Code)
	}
	// Deleting the one file leaves nothing to read.
	if w := do(h, http.MethodDelete, p, ""); w.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if w := do(h, http.MethodGet, p, ""); w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
}

// TestMalformedPathIs404: a path that is not {name}/{version}/{filename} must be
// rejected, never panic.
func TestMalformedPathIs404(t *testing.T) {
	h := newHandler(t)
	for _, p := range []string{
		"/artifacts/generic/only-a-name",
		"/artifacts/generic/a/b",
		"/artifacts/generic/a/b/c/d",
	} {
		if w := do(h, http.MethodGet, p, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", p, w.Code)
		}
	}
}

// TestMethodNotAllowed: an unsupported method is 405.
func TestMethodNotAllowed(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodPatch, "/artifacts/generic/a/1/f.bin", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("patch = %d, want 405", w.Code)
	}
}
