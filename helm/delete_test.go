package helm

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDeleteChartByFilename is a regression test.
//
// DELETE /api/charts/<chart>-<version>.tgz carries a FILENAME, but the old
// handler split it on "/" and used the whole filename as a chart NAME. So
// ListVersions found nothing, nothing was deleted, and it still answered
// {"deleted":true} with 200 - the chart kept downloading.
func TestDeleteChartByFilename(t *testing.T) {
	s := newState(t)

	for _, v := range []string{"1.0.0", "2.0.0"} {
		body := []byte("chart-" + v)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			"/artifacts/helm/api/charts?name=probe&version="+v, bytes.NewReader(body)))
		if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
			t.Fatalf("upload %s = %d %s", v, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/helm/charts/probe-1.0.0.tgz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("read 1.0.0 = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/artifacts/helm/api/charts/probe-1.0.0.tgz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/helm/charts/probe-1.0.0.tgz", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("the deleted chart still downloads (code %d)", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/helm/charts/probe-2.0.0.tgz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("deleting 1.0.0 damaged 2.0.0 = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/artifacts/helm/api/charts/nope-9.9.9.tgz", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete of a missing chart = %d, want 404", rec.Code)
	}
}
