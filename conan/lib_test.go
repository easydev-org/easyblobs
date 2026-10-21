package conan

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

// TestPingAdvertisesCapabilities: Conan v2 clients probe /ping and expect the
// capability header, so a missing header breaks every client.
func TestPingAdvertisesCapabilities(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/conan/v2/ping", "")
	if w.Code != http.StatusOK {
		t.Fatalf("ping = %d %s", w.Code, w.Body.String())
	}
	got := w.Header().Get("X-Conan-Server-Capabilities")
	if got == "" {
		t.Fatal("ping must advertise X-Conan-Server-Capabilities")
	}
	for _, want := range []string{"json", "rev2", "revisions", "checksums", "upload_zip"} {
		if !strings.Contains(got, want) {
			t.Fatalf("capability %q missing from %q", want, got)
		}
	}
}

// TestCheckCredentialsIsOpen: with auth=nil (dev) the credentials check passes.
func TestCheckCredentialsIsOpen(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodGet, "/artifacts/conan/v2/users/check_credentials", ""); w.Code != http.StatusOK {
		t.Fatalf("check_credentials = %d %s", w.Code, w.Body.String())
	}
}

// TestUnknownRouteIsProxiedOr404: an unrecognized path falls through to the
// upstream proxy, which with no upstream configured ends in 404/502 - never a
// panic and never a 200.
func TestUnknownRouteDoesNotPanic(t *testing.T) {
	h := newHandler(t)
	w := do(h, http.MethodGet, "/artifacts/conan/v2/whatever/deep/path", "")
	if w.Code == http.StatusOK {
		t.Fatalf("unknown route must not be 200: %s", w.Body.String())
	}
}
