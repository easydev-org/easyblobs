package protobuf

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWireRoundTrip pins the encoder/decoder pair the binary path relies on.
func TestWireRoundTrip(t *testing.T) {
	var b []byte
	b = wireAppendString(b, 1, "owner")
	b = wireAppendString(b, 2, "mod")
	b = wireAppendVarintField(b, 3, 7)

	fields, err := wireDecode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := firstString(fields, 1); got != "owner" {
		t.Fatalf("field 1 = %q", got)
	}
	if got := firstString(fields, 2); got != "mod" {
		t.Fatalf("field 2 = %q", got)
	}
	var v uint64
	for _, f := range fields {
		if f.Num == 3 {
			v = f.Var
		}
	}
	if v != 7 {
		t.Fatalf("field 3 = %d", v)
	}
}

// TestGetModulesBinary drives the BINARY request buf actually sends.
//
// buf's Connect client uses application/proto, not JSON - the server log shows
// ct="application/proto". A JSON-only handler answers 400 on every real push.
func TestGetModulesBinary(t *testing.T) {
	s := newFixture(t, "")

	// GetModulesRequest { module_refs: [ ModuleRef{ name: {owner, module} } ] }
	var nameMsg []byte
	nameMsg = wireAppendString(nameMsg, 1, "acme")
	nameMsg = wireAppendString(nameMsg, 2, "plug")

	var refMsg []byte
	refMsg = wireAppendMessage(refMsg, 2, nameMsg) // oneof value.name = 2

	var req []byte
	req = wireAppendMessage(req, 1, refMsg) // module_refs

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.ModuleService/GetModules", bytes.NewReader(req))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// The module does not exist yet, so the response is empty - and it must be
	// EMPTY, not an error, or buf will refuse to create it.
	if rec.Body.Len() != 0 {
		t.Fatalf("expected an empty GetModulesResponse, got %d bytes", rec.Body.Len())
	}
}

// TestUploadBinaryThenDownload pushes a module with the binary encoding buf
// uses and then reads it back through the HTML-free JSON download path.
func TestUploadBinaryThenDownload(t *testing.T) {
	s := newFixture(t, "")

	var nameMsg []byte
	nameMsg = wireAppendString(nameMsg, 1, "acme")
	nameMsg = wireAppendString(nameMsg, 2, "binmod")

	var refMsg []byte
	refMsg = wireAppendMessage(refMsg, 2, nameMsg)

	var fileMsg []byte
	fileMsg = wireAppendString(fileMsg, 1, "a.proto")
	fileMsg = wireAppendBytes(fileMsg, 2, []byte("syntax = \"proto3\";\n"))

	var contentMsg []byte
	contentMsg = wireAppendMessage(contentMsg, 1, refMsg)   // module_ref
	contentMsg = wireAppendMessage(contentMsg, 2, fileMsg)  // files

	var req []byte
	req = wireAppendMessage(req, 1, contentMsg) // contents

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.UploadService/Upload", bytes.NewReader(req))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	fields, err := wireDecode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("the response is not valid wire format: %v", err)
	}
	if len(allBytes(fields, 1)) != 1 {
		t.Fatalf("expected one commit in the response, got %d", len(allBytes(fields, 1)))
	}

	// The module now exists, so GetModules must return it.
	var nameMsg2 []byte
	nameMsg2 = wireAppendString(nameMsg2, 1, "acme")
	nameMsg2 = wireAppendString(nameMsg2, 2, "binmod")
	var refMsg2 []byte
	refMsg2 = wireAppendMessage(refMsg2, 2, nameMsg2)
	var req2 []byte
	req2 = wireAppendMessage(req2, 1, refMsg2)

	rec = httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.ModuleService/GetModules", bytes.NewReader(req2))
	r2.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r2)
	if rec.Code != http.StatusOK {
		t.Fatalf("get = %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("GetModules returned nothing after a successful upload")
	}
}
