package protobuf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newFixture(t *testing.T, upstreamURL string) *State {
	t.Helper()
	dir := t.TempDir()
	meta, err := store.OpenSQLite(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	blobs, err := store.NewFileBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	return &State{Registry: &easyblobkit.Registry{
		Blobs: blobs, Meta: meta,
		Upstreams: &easyblobkit.Upstreams{
			Defaults:  map[string]string{"protobuf": upstreamURL},
			Overrides: map[string]string{}, Proxy: map[string]string{},
		},
	}}
}

// TestConnectPassthrough verifies the root-mounted Connect call is forwarded
// verbatim (service+method at the host root), the caller's auth is preferred,
// and the response is cached.
func TestConnectPassthrough(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"modules":[]}`))
	}))
	t.Cleanup(upstream.Close)

	s := newFixture(t, upstream.URL)
	s.BSRToken = "configured-token"
	req := httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.OrganizationService/GetOrganizations",
		strings.NewReader(`{"moduleRefs":[]}`))
	req.Header.Set("Authorization", "Bearer caller-token")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/buf.alpha.registry.v1alpha1.OrganizationService/GetOrganizations" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotBody != `{"moduleRefs":[]}` {
		t.Fatalf("body = %q", gotBody)
	}
	// The caller's token wins over the configured one.
	if gotAuth != "Bearer caller-token" {
		t.Fatalf("auth = %q", gotAuth)
	}
	// Cached: a second call must not hit upstream (change the token; the cache
	// key is service+method+body, not auth).
	gotPath = ""
	req2 := httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.OrganizationService/GetOrganizations",
		strings.NewReader(`{"moduleRefs":[]}`))
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), "modules") {
		t.Fatalf("cache: %d %s", rec2.Code, rec2.Body.String())
	}
	if gotPath != "" {
		t.Fatalf("cache miss: upstream was hit again (%q)", gotPath)
	}
}

// TestConfiguredTokenWhenCallerHasNone: the deployment token is used as a
// fallback when the caller sends no Authorization.
func TestConfiguredTokenWhenCallerHasNone(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)
	s := newFixture(t, upstream.URL)
	s.BSRToken = "configured-token"
	req := httptest.NewRequest(http.MethodPost, "/artifacts/protobuf/svc/M", strings.NewReader(`{}`))
	s.ServeHTTP(httptest.NewRecorder(), req)
	if gotAuth != "Bearer configured-token" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

// TestMethodNotAllowed: PUT/DELETE are not BSR methods.
//
// GET is deliberately NOT rejected: the BSR exposes plain GET methods
// (DownloadService/Download takes query parameters and returns the module
// archive), and refusing GET made that whole surface unreachable through the
// mount while the upstream answered it with 200.
func TestMethodNotAllowed(t *testing.T) {
	s := newFixture(t, "http://127.0.0.1:1")
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(m, "/artifacts/protobuf/svc/M", nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s = %d, want 405", m, rec.Code)
		}
	}
}

// TestAirGap: with no upstream configured, the request fails closed.
func TestAirGap(t *testing.T) {
	s := newFixture(t, "")
	req := httptest.NewRequest(http.MethodPost, "/artifacts/protobuf/svc/M", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("air-gapped = %d, want 404", rec.Code)
	}
	_ = context.Background()
}
