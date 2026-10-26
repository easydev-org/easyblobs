package npm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// putDoc PUTs a CouchDB-style document and returns the recorder.
func putDoc(t *testing.T, s *State, name string, doc map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/artifacts/npm/"+name, bytes.NewReader(body)))
	return rec
}

// packument GETs the packument body.
func packument(t *testing.T, s *State, name string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/npm/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET packument = %d %s", rec.Code, rec.Body.String())
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("packument is not JSON: %v", err)
	}
	return p
}

// publishOne publishes a single-version document, as `npm publish` does.
func publishOne(t *testing.T, s *State, name, version string) {
	t.Helper()
	tb := tarball(t, name, version)
	doc := map[string]any{
		"name":     name,
		"versions": map[string]any{version: map[string]any{"name": name, "version": version}},
		"_attachments": map[string]any{
			name + "-" + version + ".tgz": map[string]any{"data": base64.StdEncoding.EncodeToString(tb)},
		},
	}
	if rec := putDoc(t, s, name, doc); rec.Code != http.StatusCreated {
		t.Fatalf("publish %s = %d %s", version, rec.Code, rec.Body.String())
	}
}

// TestDeprecateKeepsEveryVersion is the regression test for the defect the
// expanded npm suite found.
//
// `npm deprecate @scope/x@1.0.0` does not call the /deprecate endpoint. It reads
// the packument with ?write=true and PUTs the WHOLE document back, with every
// version in it and no _attachments. publish() used to walk that map, keep ONE
// version and break:
//
//	for k, v := range vs { version = k; pkgJSON = v; break }
//
// so on a package with two versions it stored a single one, dropped the
// "deprecated" flag (which lives on the version being deprecated), and answered
// 201 anyway.
func TestDeprecateKeepsEveryVersion(t *testing.T) {
	s := newState(t)
	const name = "@acme/multi"
	publishOne(t, s, name, "1.0.0")
	publishOne(t, s, name, "2.0.0")

	// What npm sends for `npm deprecate @acme/multi@1.0.0 "use 2.x"`: the whole
	// packument, every version, the flag on one of them, no attachments.
	doc := map[string]any{
		"name": name,
		"versions": map[string]any{
			"1.0.0": map[string]any{"name": name, "version": "1.0.0", "deprecated": "use 2.x"},
			"2.0.0": map[string]any{"name": name, "version": "2.0.0"},
		},
		"dist-tags": map[string]any{"latest": "2.0.0"},
	}
	if rec := putDoc(t, s, name, doc); rec.Code != http.StatusCreated {
		t.Fatalf("deprecate PUT = %d %s", rec.Code, rec.Body.String())
	}

	p := packument(t, s, name)
	vs, _ := p["versions"].(map[string]any)
	if len(vs) != 2 {
		t.Fatalf("versions after deprecate = %v, want exactly 1.0.0 and 2.0.0", keysOf(vs))
	}
	for _, want := range []string{"1.0.0", "2.0.0"} {
		if _, ok := vs[want]; !ok {
			t.Fatalf("%s disappeared; versions = %v", want, keysOf(vs))
		}
	}
	v1, _ := vs["1.0.0"].(map[string]any)
	if got, _ := v1["deprecated"].(string); got != "use 2.x" {
		t.Fatalf("versions[1.0.0].deprecated = %q, want \"use 2.x\"", got)
	}
	for k := range vs {
		if strings.TrimSpace(k) == "" {
			t.Fatalf("packument gained an empty version name: %v", keysOf(vs))
		}
	}
}

// TestMetadataOnlyPUTDoesNotClobberTheTarball: a deprecate document carries no
// _attachments, so applying it as a publish would replace a good artefact with
// nothing. The bytes must survive.
func TestMetadataOnlyPUTDoesNotClobberTheTarball(t *testing.T) {
	s := newState(t)
	const name = "@acme/keep"
	publishOne(t, s, name, "1.0.0")

	before := tarballBytes(t, s, name, "1.0.0")
	if len(before) == 0 {
		t.Fatal("tarball missing after publish")
	}

	rec := putDoc(t, s, name, map[string]any{
		"name":     name,
		"versions": map[string]any{"1.0.0": map[string]any{"name": name, "version": "1.0.0", "deprecated": "old"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("metadata PUT = %d %s", rec.Code, rec.Body.String())
	}

	after := tarballBytes(t, s, name, "1.0.0")
	if !bytes.Equal(before, after) {
		t.Fatalf("the tarball changed: %d bytes before, %d after", len(before), len(after))
	}
}

// tarballBytes fetches the tarball the packument points at.
func tarballBytes(t *testing.T, s *State, name, version string) []byte {
	t.Helper()
	p := packument(t, s, name)
	vs, _ := p["versions"].(map[string]any)
	v, _ := vs[version].(map[string]any)
	dist, _ := v["dist"].(map[string]any)
	u, _ := dist["tarball"].(string)
	if u == "" {
		return nil
	}
	if i := strings.Index(u, "/artifacts/npm/"); i >= 0 {
		u = u[i:]
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET tarball %s = %d", u, rec.Code)
	}
	return rec.Body.Bytes()
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
