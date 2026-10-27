package arch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newHandler(t *testing.T) http.Handler {
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

func do(h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestSplitPkgFilename: the split is from the RIGHT, so a package name that
// contains dashes survives intact. Splitting from the left would turn
// python-setuptools-scm into a package called "python".
func TestSplitPkgFilename(t *testing.T) {
	cases := []struct {
		in             string
		name, version  string
		arch           string
		refuseExpected bool
	}{
		{"acl-2.4.0-1-x86_64.pkg.tar.zst", "acl", "2.4.0-1", "x86_64", false},
		{"python-setuptools-scm-8.0.4-1-any.pkg.tar.zst",
			"python-setuptools-scm", "8.0.4-1", "any", false},
		{"gcc-14.2.1+r134+gab884fffe3fc-1-x86_64.pkg.tar.zst",
			"gcc", "14.2.1+r134+gab884fffe3fc-1", "x86_64", false},
		{"foo-1.0-1-x86_64.pkg.tar.xz", "foo", "1.0-1", "x86_64", false},
		// Not a package: too few fields to be name-ver-rel-arch.
		{"notapackage.txt", "", "", "", true},
		{"a-b.pkg.tar.zst", "", "", "", true},
	}
	for _, c := range cases {
		name, ver, archName := splitPkgFilename(c.in)
		if c.refuseExpected {
			if name != "" || ver != "" || archName != "" {
				t.Errorf("%q: expected refusal, got (%q,%q,%q)", c.in, name, ver, archName)
			}
			continue
		}
		if name != c.name || ver != c.version || archName != c.arch {
			t.Errorf("%q: got (%q,%q,%q), want (%q,%q,%q)",
				c.in, name, ver, archName, c.name, c.version, c.arch)
		}
	}
}

// TestHostedDBRoundTrip is the reason the generator exists: publish packages,
// then read the database pacman reads.
func TestHostedDBRoundTrip(t *testing.T) {
	h := newHandler(t)
	const repo = "core"
	const archName = "x86_64"

	pkg := []byte("pretend this is a .pkg.tar.zst")
	fn := "ezhello-1.0.0-1-" + archName + ".pkg.tar.zst"

	w := do(h, http.MethodPut, "/artifacts/arch/_hosted/e2e/"+repo+"/os/"+archName+"/"+fn, pkg)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("PUT package = %d %s", w.Code, w.Body.String())
	}

	// The database is generated under os/<arch>/ with two names.
	for _, name := range []string{repo + ".db", repo + ".db.tar.gz"} {
		w = do(h, http.MethodGet,
			"/artifacts/arch/_hosted/e2e/"+repo+"/os/"+archName+"/"+name, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", name, w.Code, w.Body.String())
		}
		body := w.Body.Bytes()
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s is not gzip: %v", name, err)
		}
		tr := tar.NewReader(zr)
		var sawDesc bool
		var desc string
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%s: tar: %v", name, err)
			}
			if strings.HasSuffix(hdr.Name, "/desc") {
				sawDesc = true
				b, _ := io.ReadAll(tr)
				desc = string(b)
			}
		}
		if !sawDesc {
			t.Fatalf("%s has no */desc member", name)
		}
		// pacman needs FILENAME to find the package and SHA256SUM to verify it.
		for _, want := range []string{"%FILENAME%", fn, "%NAME%", "ezhello", "%SHA256SUM%"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s desc missing %q:\n%s", name, want, desc)
			}
		}
	}
}

// TestHostedDBEntryNameIsNameVersion: the tar member directory is
// <name>-<version>, which is what pacman keys the entry by.
func TestHostedDBEntryNameIsNameVersion(t *testing.T) {
	h := newHandler(t)
	const repo, archName = "core", "x86_64"
	fn := "acl-2.4.0-1-" + archName + ".pkg.tar.zst"

	if w := do(h, http.MethodPut,
		"/artifacts/arch/_hosted/e2e/"+repo+"/os/"+archName+"/"+fn, []byte("x")); w.Code >= 300 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	w := do(h, http.MethodGet, "/artifacts/arch/_hosted/e2e/"+repo+"/os/"+archName+"/"+repo+".db", nil)
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var dirs []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if hdr.Typeflag == tar.TypeDir {
			dirs = append(dirs, strings.TrimSuffix(hdr.Name, "/"))
		}
	}
	if len(dirs) != 1 || dirs[0] != "acl-2.4.0-1" {
		t.Fatalf("entry directories = %v, want [acl-2.4.0-1]", dirs)
	}
}

// TestHostedDeleteRemovesIt: DELETE is part of the hosted protocol.
func TestHostedDeleteRemovesIt(t *testing.T) {
	h := newHandler(t)
	const p = "/artifacts/arch/_hosted/e2e/core/os/x86_64/ezhello-1.0.0-1-x86_64.pkg.tar.zst"
	if w := do(h, http.MethodPut, p, []byte("x")); w.Code >= 300 {
		t.Fatalf("PUT = %d", w.Code)
	}
	if w := do(h, http.MethodDelete, p, nil); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body.String())
	}
	if w := do(h, http.MethodGet, p, nil); w.Code == http.StatusOK {
		t.Fatal("the deleted package is still served")
	}
}

// TestNonCachePathRefused: the proxy side stays read-only.
func TestNonCachePathRefused(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodPut, "/artifacts/arch/core/os/x86_64/x.pkg.tar.zst", []byte("x")); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT into the proxy namespace = %d, want 405", w.Code)
	}
}

// TestGeneratedDBIsValidTar guards the container: pacman reading a corrupt db
// reports a repository error, not a missing package, so the shape matters.
func TestGeneratedDBIsValidTar(t *testing.T) {
	entries := []pkgEntry{
		{Filename: "a-1-1-x86_64.pkg.tar.zst", Name: "a", Version: "1-1", CSize: 5, SHA256: "deadbeef"},
		{Filename: "b-2-1-x86_64.pkg.tar.zst", Name: "b", Version: "2-1", CSize: 6, SHA256: "cafebabe"},
	}
	raw := buildDBTar(entries)
	tr := tar.NewReader(bytes.NewReader(raw))
	n := 0
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		n++
	}
	// Two packages x (dir + desc).
	if n != 4 {
		t.Fatalf("members = %d, want 4", n)
	}
    _ = context.Background
}
