package cargo

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPublishHonoursQueryVersion is a regression test.
//
// `PUT /api/v1/crates/new?name=X&version=1.0.0` used to honour the NAME from the
// query string and silently ignore the VERSION, which then fell through to the
// 0.1.0 default:
//
//	name, version, crate := parsePublishBody(data)   // both "" for a plain .crate
//	if name == "" { name = query.Get("name") }      // handled
//	if version == "" { version = "0.1.0" }          // <- query never consulted
//
// So a client published 1.0.0, got 201, and then could not find it: the stored
// version was 0.1.0 and the download 404'd (or hung trying the upstream). That
// reads as "the registry lost my upload", which is the worst kind of wrong.
func TestPublishHonoursQueryVersion(t *testing.T) {
	s := newState(t)

	body := []byte("\x1f\x8b\x08\x00not-a-real-crate") // irrelevant: name/version come from the query
	req := httptest.NewRequest(http.MethodPut,
		"/artifacts/cargo/api/v1/crates/new?name=acme-hello&version=1.0.0",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d: %s", rec.Code, rec.Body.String())
	}

	vs, err := s.Registry.Meta.ListVersions(req.Context(), "cargo", "acme-hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("expected one stored version, got %v", vs)
	}
	if vs[0] != "1.0.0" {
		t.Fatalf("stored version = %q, want 1.0.0 (the default 0.1.0 leaked through)", vs[0])
	}
}
