package debian

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newTestHandler(t *testing.T) http.Handler {
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
	h, err := NewHandler(reg, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestGeneratedPackagesHasByHash checks the hosted generator emits a by-hash
// alias for Packages and that the handler serves it byte-identically.
func TestGeneratedPackagesHasByHash(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/artifacts/debian/_hosted/r/pool/f.deb", bytes.NewReader([]byte("deb")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT=%d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/artifacts/debian/_hosted/r/Packages", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Packages=%d", rec.Code)
	}
	pk := rec.Body.Bytes()

	sum := sha256Hex(pk)
	alias := "by-hash/SHA256/" + sum
	req = httptest.NewRequest(http.MethodGet, "/artifacts/debian/_hosted/r/"+alias, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("by-hash GET=%d (alias=%s)", rec.Code, alias)
	}
	if !bytes.Equal(rec.Body.Bytes(), pk) {
		t.Fatalf("by-hash body differs from Packages")
	}
}

// TestByHashParse checks the path parser.
func TestByHashParse(t *testing.T) {
	cases := []struct {
		in   string
		algo string
		ok   bool
	}{
		{"dists/bookworm/main/binary-amd64/by-hash/SHA256/" + strings.Repeat("a", 64), "sha256", true},
		{"dists/bookworm/main/binary-amd64/Packages.gz", "", false},
		{"by-hash/SHA256/xyz", "", false},
	}
	for _, c := range cases {
		algo, _, ok := parseByHash(c.in)
		if ok != c.ok || algo != c.algo {
			t.Errorf("parseByHash(%q) = %q,%v want %q,%v", c.in, algo, ok, c.algo, c.ok)
		}
	}
}
