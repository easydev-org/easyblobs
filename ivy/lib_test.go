package ivy

import (
	"io"
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

// TestPutGetDeleteRoundTrip: Ivy's contract is {host}/{path} -> bytes. The host
// is the first segment (a repo like repo.scala-sbt.org) and the rest is the
// artifact path, so this pins the SplitRepo behaviour callers rely on.
func TestPutGetDeleteRoundTrip(t *testing.T) {
	h := newHandler(t)
	// A two-segment path (host + one file) is the minimum SplitRepo accepts.
	const p = "/artifacts/ivy/repo.example.org/org.example/thing-1.0.jar"
	if w := do(h, http.MethodPut, p, "JAR-BYTE"); w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("put = %d %s", w.Code, w.Body.String())
	}
	w := do(h, http.MethodGet, p, "")
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d %s", w.Code, w.Body.String())
	}
	got, _ := io.ReadAll(w.Body)
	if string(got) != "JAR-BYTE" {
		t.Fatalf("bytes = %q", string(got))
	}
	if w := do(h, http.MethodDelete, p, ""); w.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
}

// TestMissingHostOrPathIs404: an empty host or an empty file path must be
// rejected with a clear 404, not treated as a root request.
func TestMissingHostOrPathIs404(t *testing.T) {
	h := newHandler(t)
	for _, p := range []string{
		"/artifacts/ivy/",
		"/artifacts/ivy/hostonly",
	} {
		if w := do(h, http.MethodGet, p, ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", p, w.Code)
		}
	}
}

// TestMethodNotAllowed: an unsupported method is 405.
func TestMethodNotAllowed(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodPatch, "/artifacts/ivy/host/a/b.jar", ""); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("patch = %d, want 405", w.Code)
	}
}
