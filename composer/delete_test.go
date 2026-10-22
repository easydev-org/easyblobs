package composer

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeleteRemovesOnlyTheNamedVersion is a regression test for a delete that
// silently did nothing.
//
// The path is /api/packages/{vendor}/{package}/{version} and the package NAME
// itself contains a slash. The old handler treated the WHOLE path as the name,
// so ListVersions("composer", "acme/lib/1.0.0") found nothing, the loop deleted
// nothing, and it still answered {"ok":true} with 200 - the p2 index kept
// advertising the "deleted" version.
func TestDeleteRemovesOnlyTheNamedVersion(t *testing.T) {
	s := newState(t)

	for _, v := range []string{"1.0.0", "2.0.0"} {
		body := []byte(`{"name":"acme/lib","version":"` + v + `"}`)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/artifacts/composer/api/packages", bytes.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s = %d %s", v, rec.Code, rec.Body.String())
		}
	}

	// Both versions must be advertised first.
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/composer/p2/acme/lib.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("p2 = %d %s", rec.Code, rec.Body.String())
	}
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if !strings.Contains(rec.Body.String(), `"`+v+`"`) {
			t.Fatalf("p2 does not list %s: %s", v, rec.Body.String())
		}
	}

	// Delete exactly 1.0.0.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/artifacts/composer/api/packages/acme/lib/1.0.0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/composer/p2/acme/lib.json", nil))
	if strings.Contains(rec.Body.String(), `"1.0.0"`) {
		t.Fatalf("the p2 index still advertises the deleted 1.0.0: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"2.0.0"`) {
		t.Fatalf("deleting 1.0.0 also removed 2.0.0: %s", rec.Body.String())
	}

	// Deleting the same version again is a 404, not a second 200.
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/artifacts/composer/api/packages/acme/lib/1.0.0", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}

// TestDeleteWholePackage covers the no-version form, which must remove every
// release rather than nothing.
func TestDeleteWholePackage(t *testing.T) {
	s := newState(t)
	for _, v := range []string{"1.0.0", "2.0.0"} {
		body := []byte(`{"name":"acme/all","version":"` + v + `"}`)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut,
			"/artifacts/composer/api/packages", bytes.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("upload %s = %d", v, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete,
		"/artifacts/composer/api/packages/acme/all", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete package = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/composer/p2/acme/all.json", nil))
	for _, v := range []string{"1.0.0", "2.0.0"} {
		if strings.Contains(rec.Body.String(), `"`+v+`"`) {
			t.Fatalf("p2 still lists %s after deleting the package: %s", v, rec.Body.String())
		}
	}
}
