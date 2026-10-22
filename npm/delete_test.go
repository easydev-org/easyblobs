package npm

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeleteScopedPackageRemovesEveryVersion is a regression test.
//
// A scoped npm name ALWAYS contains a slash (@scope/name). The old deleteName
// split the name on the LAST "/" and treated the tail as a version:
//
//	"@e2e/pkg" -> name "@e2e", version "pkg"
//
// so it removed nothing from "@e2e/pkg" - and, worse, could have attacked a
// different package called "@e2e" - while answering {"ok":true} with 200. The
// packument therefore kept resolving after a "successful" delete.
func TestDeleteScopedPackageRemovesEveryVersion(t *testing.T) {
	s := newState(t)
	name := "@acme/pkg"
	enc := "@acme%2Fpkg"

	for _, v := range []string{"1.0.0", "2.0.0"} {
		doc := `{"name":"` + name + `","versions":{"` + v +
			`":{"name":"` + name + `","version":"` + v +
			`","dist":{"tarball":"http://x/` + v + `.tgz"}}},"dist-tags":{"latest":"` + v + `"}}`
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/artifacts/npm/"+enc, bytes.NewReader([]byte(doc))))
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("publish %s = %d %s", v, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/"+enc, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("packument = %d %s", rec.Code, rec.Body.String())
	}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(rec.Body.String(), `"`+v+`"`) {
			t.Fatalf("packument does not list %s: %s", v, rec.Body.String())
		}
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/artifacts/npm/"+enc, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/"+enc, nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("the packument still resolves after delete (code %d, body %s)",
			rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/artifacts/npm/"+enc, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}
