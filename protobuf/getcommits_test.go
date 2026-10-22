package protobuf

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pushModule uploads a module through the binary API buf uses and returns the
// stored (full) version string.
func pushModule(t *testing.T, s *State, owner, module string) string {
	t.Helper()

	var nameMsg []byte
	nameMsg = wireAppendString(nameMsg, 1, owner)
	nameMsg = wireAppendString(nameMsg, 2, module)
	var refMsg []byte
	refMsg = wireAppendMessage(refMsg, 2, nameMsg) // ModuleRef.value.name

	var fileMsg []byte
	fileMsg = wireAppendString(fileMsg, 1, "a.proto")
	fileMsg = wireAppendBytes(fileMsg, 2, []byte("syntax = \"proto3\";\n"))

	var contentMsg []byte
	contentMsg = wireAppendMessage(contentMsg, 1, refMsg)  // Content.module_ref
	contentMsg = wireAppendMessage(contentMsg, 2, fileMsg) // Content.files

	var upReq []byte
	upReq = wireAppendMessage(upReq, 1, contentMsg) // UploadRequest.contents

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.UploadService/Upload", bytes.NewReader(upReq))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}

	vs, err := s.Registry.Meta.ListVersions(r.Context(), "protobuf", owner+"/"+module)
	if err != nil || len(vs) != 1 {
		t.Fatalf("expected one stored version for %s/%s, got %v (err %v)",
			owner, module, vs, err)
	}
	return vs[0]
}

// getCommits issues GetCommits with a ResourceRef and returns how many commits
// came back.
//
// The request shape is the REAL one, from commit_service.proto and
// resource.proto:
//
//	GetCommitsRequest { repeated ResourceRef resource_refs = 1; }
//	ResourceRef { oneof value { string id = 1; Name name = 2 } }
//	ResourceRef.Name {
//	  string owner = 1; string module = 2;
//	  oneof child { string label_name = 3; string ref = 4; }
//	}
func getCommits(t *testing.T, s *State, owner, module, ref, label, id string) int {
	t.Helper()

	var rr []byte
	if id != "" {
		rr = wireAppendString(rr, 1, id) // value.id
	} else {
		var name []byte
		name = wireAppendString(name, 1, owner)
		name = wireAppendString(name, 2, module)
		if label != "" {
			name = wireAppendString(name, 3, label)
		}
		if ref != "" {
			name = wireAppendString(name, 4, ref)
		}
		rr = wireAppendMessage(rr, 2, name) // value.name
	}

	var req []byte
	req = wireAppendMessage(req, 1, rr) // resource_refs

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/buf.registry.module.v1.CommitService/GetCommits", bytes.NewReader(req))
	r.Header.Set("Content-Type", "application/proto")
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("getcommits = %d: %s", rec.Code, rec.Body.String())
	}
	fields, err := wireDecode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("response is not wire format: %v", err)
	}
	return len(allBytes(fields, 1)) // GetCommitsResponse.commits
}

// TestGetCommitsByRef pins the shape `buf registry module commit info` sends: a
// ResourceRef Name with owner/module and child.ref = the commit id.
//
// The previous implementation expected commit_refs/CommitRef.Full - a DIFFERENT
// message - so every request decoded into zero refs and buf reported
// "expect 1 commit from response, got 0".
func TestGetCommitsByRef(t *testing.T) {
	s := newFixture(t, "")
	full := pushModule(t, s, "acme", "byref")
	short := full[:32]

	if n := getCommits(t, s, "acme", "byref", short, "", ""); n != 1 {
		t.Fatalf("ResourceRef.Name with child.ref = %s resolved %d commit(s), want 1", short, n)
	}
}

// TestGetCommitsByLabel covers the child.label_name form.
func TestGetCommitsByLabel(t *testing.T) {
	s := newFixture(t, "")
	full := pushModule(t, s, "acme", "bylabel")
	short := full[:32]

	if n := getCommits(t, s, "acme", "bylabel", "", short, ""); n != 1 {
		t.Fatalf("ResourceRef.Name with child.label_name resolved %d commit(s), want 1", n)
	}
}

// TestGetCommitsByID covers the id-only form, where no module is named and the
// commit must be found across the format.
func TestGetCommitsByID(t *testing.T) {
	s := newFixture(t, "")
	full := pushModule(t, s, "acme", "byid")
	short := full[:32]

	if n := getCommits(t, s, "", "", "", "", short); n != 1 {
		t.Fatalf("ResourceRef.id resolved %d commit(s), want 1", n)
	}
}

// TestGetCommitsUnknownIsEmpty makes sure a ref that does not exist yields an
// empty list rather than a phantom commit.
func TestGetCommitsUnknownIsEmpty(t *testing.T) {
	s := newFixture(t, "")
	pushModule(t, s, "acme", "unknown")

	if n := getCommits(t, s, "acme", "unknown", "00000000000000000000000000000000", "", ""); n != 0 {
		t.Fatalf("an unknown commit id resolved %d commit(s), want 0", n)
	}
}
