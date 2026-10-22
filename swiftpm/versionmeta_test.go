package swift

import (
	"net/http"
	"strings"
	"testing"
)

// TestDeletedReleaseIsNotFound is a regression test.
//
// versionMeta used to swallow the Meta.Get error and answer 200 anyway, with a
// release whose checksum was the EMPTY STRING. After a DELETE the release
// therefore still "resolved": a real client would then try to download a
// source archive whose checksum does not exist, instead of being told plainly
// that the release is gone.
func TestDeletedReleaseIsNotFound(t *testing.T) {
	h := newHandler(t)

	if rec := do(h, http.MethodPut, "/artifacts/swift/acme/probe/1.0.0", "archive-1"); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body.String())
	}

	rec := do(h, http.MethodGet, "/artifacts/swift/acme/probe/1.0.0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"checksum":""`) {
		t.Fatalf("the release metadata carries an empty checksum: %s", rec.Body.String())
	}

	if rec := do(h, http.MethodDelete, "/artifacts/swift/acme/probe/1.0.0", ""); rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}

	rec = do(h, http.MethodGet, "/artifacts/swift/acme/probe/1.0.0", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("read after delete = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
