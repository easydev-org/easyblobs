package pub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// targz builds a minimal package archive whose pubspec.yaml names name@version.
func targz(t *testing.T, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := "name: " + name + "\nversion: " + version +
		"\nenvironment:\n  sdk: '>=3.0.0 <4.0.0'\n"
	hdr := &tar.Header{Name: "pubspec.yaml", Mode: 0o644, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDeleteVersionRemovesOnlyThatVersion is the regression test for the defect
// the expanded pub suite found.
//
// DELETE /api/packages/<name>/versions/<ver> was matched by the
// ".../versions/" case and handed to versionArchive, which serves the ARCHIVE
// and has no DELETE branch. The request answered 200 and nothing was removed,
// so the version stayed advertised - the same shape as the composer/helm/npm
// delete bugs.
func TestDeleteVersionRemovesOnlyThatVersion(t *testing.T) {
	h := newHandler(t)
	const name = "ezpkg"

	// Two versions, so "deleted the right one" is a real assertion.
	for _, v := range []string{"1.0.0", "2.0.0"} {
		body := targz(t, name, v)
		req := httptest.NewRequest(http.MethodPost,
			"/artifacts/pub/api/packages/versions/newUpload?name="+name+"&version="+v,
			bytes.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("upload %s = %d %s", v, w.Code, w.Body.String())
		}
	}

	got := do(h, http.MethodGet, "/artifacts/pub/api/packages/"+name, "")
	if got.Code != http.StatusOK {
		t.Fatalf("metadata = %d %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "1.0.0") {
		t.Fatalf("1.0.0 was never stored: %s", got.Body.String())
	}

	del := do(h, http.MethodDelete,
		"/artifacts/pub/api/packages/"+name+"/versions/1.0.0", "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", del.Code, del.Body.String())
	}

	after := do(h, http.MethodGet, "/artifacts/pub/api/packages/"+name, "").Body.String()
	if strings.Contains(after, `"1.0.0"`) {
		t.Fatalf("the deleted version is still advertised: %s", after)
	}
	if !strings.Contains(after, "2.0.0") {
		t.Fatalf("deleting 1.0.0 took 2.0.0 with it: %s", after)
	}
}
