package git

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpstreamNotFound locks the classification of a clone failure: an upstream
// "repository not found" is a definitive 404 (client can fall back), while a
// transport/auth failure is not.
func TestUpstreamNotFound(t *testing.T) {
	cases := map[string]bool{
		"remote: Repository not found.\nfatal: Authentication failed": true,
		"fatal: repository 'https://x/y.git/' not found":              true,
		"fatal: could not read Username for 'https://github.com'":     false,
		"fatal: unable to access: Could not resolve host":             false,
		"fatal: the remote end hung up unexpectedly":                  false,
	}
	for out, want := range cases {
		if got := upstreamNotFound(out); got != want {
			t.Errorf("upstreamNotFound(%q) = %v want %v", out, got, want)
		}
	}
}

// TestMirrorErrMaps404 verifies a NotFound gitError becomes a 404 (not 502)
// with a clear body, and other errors stay 502.
func TestMirrorErrMaps404(t *testing.T) {
	s := &State{}

	rec := httptest.NewRecorder()
	s.mirrorErr(rec, &gitError{Op: "clone", Err: errStr("exit 128"), Out: "remote: Repository not found.", NotFound: true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("notfound: code=%d want 404", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	s.mirrorErr(rec2, &gitError{Op: "clone", Err: errStr("exit 128"), Out: "dial tcp: refused"})
	if rec2.Code != http.StatusBadGateway {
		t.Fatalf("transport: code=%d want 502", rec2.Code)
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
