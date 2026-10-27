package nix

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// hostedNix builds a Handler through NewHandler, which is what installs the
// Hosted field. Constructing &State{} directly - as the pull-through test does -
// leaves it nil, so it cannot exercise the upload path at all.
func hostedNix(t *testing.T) http.Handler {
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
	reg := &easyblobkit.Registry{
		Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta),
		Upstreams: &easyblobkit.Upstreams{
			Defaults: map[string]string{}, Overrides: map[string]string{},
			Proxy: map[string]string{},
		},
	}
	h, err := NewHandler(reg, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestHostedNarinfoRoundTrip is the regression test for the missing write path.
//
// The adapter answered 405 to every non-GET, so `nix copy --to <mount>` - the
// protocol's own upload command - could not work at all, and the suite recorded
// that as "read-only by design". A binary cache is not read-only: nix-serve,
// Cachix and Attic all accept PUT of the same narinfo/nar pair a GET reads.
func TestHostedNarinfoRoundTrip(t *testing.T) {
	h := hostedNix(t)
	const repo = "e2e"

	narinfo := "StorePath: /nix/store/" + testHash + "-hello-2.12.1\n" +
		"URL: nar/" + testHash + ".nar.md5\n" +
		"NarHash: sha256:0000\nNarSize: 42\n"
	nar := []byte("fake-nar-bytes-for-hosted")

	// Publish the narinfo.
	req := httptest.NewRequest(http.MethodPut,
		"/artifacts/nix/_hosted/"+repo+"/"+testHash+".narinfo",
		strings.NewReader(narinfo))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("PUT narinfo = %d %s", w.Code, w.Body.String())
	}

	// Publish the nar it points at.
	req = httptest.NewRequest(http.MethodPut,
		"/artifacts/nix/_hosted/"+repo+"/nar/"+testHash+".nar.md5",
		bytes.NewReader(nar))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("PUT nar = %d %s", w.Code, w.Body.String())
	}

	// Read both back byte-for-byte.
	req = httptest.NewRequest(http.MethodGet,
		"/artifacts/nix/_hosted/"+repo+"/"+testHash+".narinfo", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET narinfo = %d %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != narinfo {
		t.Fatalf("narinfo came back changed:\n got %q\nwant %q", got, narinfo)
	}

	req = httptest.NewRequest(http.MethodGet,
		"/artifacts/nix/_hosted/"+repo+"/nar/"+testHash+".nar.md5", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET nar = %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), nar) {
		t.Fatalf("nar bytes differ: got %d bytes, want %d", w.Body.Len(), len(nar))
	}
}

// TestHostedRefusesPathsAClientCannotRead: the write side accepts exactly the
// shapes the read side accepts, so an upload cannot create a key that a nix
// client would then refuse to fetch.
func TestHostedRefusesPathsAClientCannotRead(t *testing.T) {
	h := hostedNix(t)
	for _, bad := range []string{"some/other/path", testHash + ".txt", "nar/short.nar"} {
		req := httptest.NewRequest(http.MethodPut,
			"/artifacts/nix/_hosted/e2e/"+bad, strings.NewReader("x"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("PUT %q = %d, want 404 (the read side would refuse it too)", bad, w.Code)
		}
	}
}

// TestHostedCacheInfoIsNotWritable: nix-cache-info describes the MOUNT (its
// StoreDir), not a file a client may replace.
func TestHostedCacheInfoIsNotWritable(t *testing.T) {
	h := hostedNix(t)
	req := httptest.NewRequest(http.MethodPut,
		"/artifacts/nix/_hosted/e2e/nix-cache-info",
		strings.NewReader("StoreDir: /somewhere/else\n"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT nix-cache-info = %d, want 405", w.Code)
	}
	// ...but reading it still answers with the mount's own.
	req = httptest.NewRequest(http.MethodGet,
		"/artifacts/nix/_hosted/e2e/nix-cache-info", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "StoreDir: /nix/store") {
		t.Fatalf("GET nix-cache-info = %d %q", w.Code, w.Body.String())
	}
}

// TestHostedDeleteRemovesIt: DELETE is part of the upload protocol.
func TestHostedDeleteRemovesIt(t *testing.T) {
	h := hostedNix(t)
	const path = "/artifacts/nix/_hosted/e2e/" + testHash + ".narinfo"

	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader("StorePath: x\n"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, path, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, path, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("the deleted narinfo is still served: %q", w.Body.String())
	}
}
