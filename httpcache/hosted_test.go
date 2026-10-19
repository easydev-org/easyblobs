package httpcache

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newState(t *testing.T, tree string) http.Handler {
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
	reg := &easyblobkit.Registry{Blobs: blobs, Meta: meta,
		Upstreams: &easyblobkit.Upstreams{Defaults: map[string]string{}, Overrides: map[string]string{}, Proxy: map[string]string{}}}
	h, err := newHandler(tree)(reg, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestHostedCRAN checks a hosted CRAN repo accepts an upload and generates the
// PACKAGES index from it.
func TestHostedCRAN(t *testing.T) {
	h := newState(t, "cran")
	body := []byte("R source package bytes")
	req := httptest.NewRequest(http.MethodPut, "/artifacts/cran/_hosted/myrepo/src/contrib/mypkg_1.0.0.tar.gz", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT = %d (%s)", rec.Code, rec.Body.String())
	}
	// The generated PACKAGES lists the uploaded package.
	req = httptest.NewRequest(http.MethodGet, "/artifacts/cran/_hosted/myrepo/src/contrib/PACKAGES", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PACKAGES = %d", rec.Code)
	}
	got := rec.Body.String()
	if !strings.Contains(got, "Package: mypkg") || !strings.Contains(got, "Version: 1.0.0") {
		t.Fatalf("PACKAGES = %q", got)
	}
	// The uploaded file round-trips byte-identically.
	req = httptest.NewRequest(http.MethodGet, "/artifacts/cran/_hosted/myrepo/src/contrib/mypkg_1.0.0.tar.gz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("download = %d %q", rec.Code, rec.Body.String())
	}
}

// TestHostedCPAN checks the CPAN index is generated (gzipped).
func TestHostedCPAN(t *testing.T) {
	h := newState(t, "cpan")
	req := httptest.NewRequest(http.MethodPut, "/artifacts/cpan/_hosted/priv/authors/id/A/AB/ABC/Foo-1.0.tar.gz", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/artifacts/cpan/_hosted/priv/modules/02packages.details.txt.gz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index = %d", rec.Code)
	}
}

// TestHostedPullThroughStillReadOnly checks the non-hosted path stays a
// read-only cache (a PUT is rejected).
func TestHostedPullThroughStillReadOnly(t *testing.T) {
	h := newState(t, "cran")
	req := httptest.NewRequest(http.MethodPut, "/artifacts/cran/src/contrib/x.tar.gz", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-hosted PUT = %d, want 405", rec.Code)
	}
	_ = context.Background()
}
