package protobuf

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetCommitsResolvesIDOnly covers the shape `buf registry module commit info`
// actually sends: a CommitRef carrying ONLY the id, with no module reference.
//
// Restricting the lookup to a module taken from the request found nothing for
// this form, which is why buf kept saying
// "expect 1 commit from response, got 0" even after the Full form worked.
func TestGetCommitsResolvesIDOnly(t *testing.T) {
	s := newFixture(t, "")

	var nameMsg []byte
	nameMsg = wireAppendString(nameMsg, 1, "acme")
	nameMsg = wireAppendString(nameMsg, 2, "idonly")
	var refMsg []byte
	refMsg = wireAppendMessage(refMsg, 2, nameMsg)
	var fileMsg []byte
	fileMsg = wireAppendString(fileMsg, 1, "a.proto")
	fileMsg = wireAppendBytes(fileMsg, 2, []byte("syntax = \"proto3\";\n"))
	var contentMsg []byte
	contentMsg = wireAppendMessage(contentMsg, 1, refMsg)
	contentMsg = wireAppendMessage(contentMsg, 2, fileMsg)
	var upReq []byte
	upReq = wireAppendMessage(upReq, 1, contentMsg)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.UploadService/Upload", bytes.NewReader(upReq))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}

	vs, err := s.Registry.Meta.ListVersions(r.Context(), "protobuf", "acme/idonly")
	if err != nil || len(vs) != 1 {
		t.Fatalf("expected one stored version, got %v", vs)
	}
	short := vs[0][:32]

	// CommitRef.value { id = <short> } - no module at all.
	var valueMsg []byte
	valueMsg = wireAppendString(valueMsg, 1, short) // value.id
	var commitRef []byte
	commitRef = wireAppendMessage(commitRef, 1, valueMsg)
	var req []byte
	req = wireAppendMessage(req, 1, commitRef)

	rec = httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.CommitService/GetCommits", bytes.NewReader(req))
	r2.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r2)
	if rec.Code != http.StatusOK {
		t.Fatalf("getcommits = %d: %s", rec.Code, rec.Body.String())
	}
	fields, err := wireDecode(rec.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(allBytes(fields, 1)); got != 1 {
		t.Fatalf("an id-only CommitRef must resolve; got %d commit(s)", got)
	}
}

// TestGetCommitsResolvesShortID is a regression test.
//
// Upload returns commit[:32] as the commit id, because buf requires a dashless
// uuid of exactly 32 characters. The store keys the version by the FULL
// 128-char SHAKE256 digest. An exact lookup with the short id found nothing, and
// buf reported:
//
//	expect 1 commit from response, got 0
func TestGetCommitsResolvesShortID(t *testing.T) {
	s := newFixture(t, "")

	// Push a module so there is a commit to resolve.
	var nameMsg []byte
	nameMsg = wireAppendString(nameMsg, 1, "acme")
	nameMsg = wireAppendString(nameMsg, 2, "gcmod")
	var refMsg []byte
	refMsg = wireAppendMessage(refMsg, 2, nameMsg)
	var fileMsg []byte
	fileMsg = wireAppendString(fileMsg, 1, "a.proto")
	fileMsg = wireAppendBytes(fileMsg, 2, []byte("syntax = \"proto3\";\n"))
	var contentMsg []byte
	contentMsg = wireAppendMessage(contentMsg, 1, refMsg)
	contentMsg = wireAppendMessage(contentMsg, 2, fileMsg)
	var upReq []byte
	upReq = wireAppendMessage(upReq, 1, contentMsg)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.UploadService/Upload", bytes.NewReader(upReq))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}

	// The stored version is the full digest; the client sees only its prefix.
	vs, err := s.Registry.Meta.ListVersions(r.Context(), "protobuf", "acme/gcmod")
	if err != nil || len(vs) != 1 {
		t.Fatalf("expected one stored version, got %v (err %v)", vs, err)
	}
	full := vs[0]
	short := full[:32]

	// GetCommits with CommitRef.Full { module_ref, commit = short }
	var shortMsg []byte
	shortMsg = wireAppendString(shortMsg, 1, "acme")
	shortMsg = wireAppendString(shortMsg, 2, "gcmod")
	var mref []byte
	mref = wireAppendMessage(mref, 2, shortMsg) // ModuleRef.value.name

	var fullMsg []byte
	fullMsg = wireAppendMessage(fullMsg, 1, mref)         // Full.module_ref
	fullMsg = wireAppendString(fullMsg, 2, short)         // Full.commit

	var valueMsg []byte
	valueMsg = wireAppendMessage(valueMsg, 2, fullMsg)    // CommitRef.value.full

	var commitRef []byte
	commitRef = wireAppendMessage(commitRef, 1, valueMsg) // CommitRef

	var req []byte
	req = wireAppendMessage(req, 1, commitRef) // GetCommitsRequest.commit_refs

	rec = httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.CommitService/GetCommits", bytes.NewReader(req))
	r2.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r2)
	if rec.Code != http.StatusOK {
		t.Fatalf("getcommits = %d: %s", rec.Code, rec.Body.String())
	}
	fields, err := wireDecode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("response is not wire format: %v", err)
	}
	if got := len(allBytes(fields, 1)); got != 1 {
		t.Fatalf("expected exactly 1 commit in the response, got %d "+
			"(this is the 'expect 1 commit from response, got 0' failure)", got)
	}
}
