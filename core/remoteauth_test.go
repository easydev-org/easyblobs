package easyblobkit

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeWhoami is a minimal easyvcs identity endpoint: it authenticates a fixed
// set of tokens.
func fakeWhoami(tokens map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		if len(tok) > 7 && tok[:7] == "Bearer " {
			tok = tok[7:]
		}
		if u, ok := tokens[tok]; ok {
			_, _ = w.Write([]byte(`{"id":1,"username":"` + u + `","authenticated":true}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{}`))
	}))
}

func TestRemoteAuthIdentity(t *testing.T) {
	srv := fakeWhoami(map[string]string{"good": "t-acme"})
	defer srv.Close()
	a := NewRemoteAuth(srv.URL)
	ctx := context.Background()

	// A valid bearer maps to the easyvcs username.
	if u, ok := a.CheckBearer(ctx, "good", "repository:acme/app:pull"); !ok || u != "t-acme" {
		t.Fatalf("CheckBearer(good, pull) = %q,%v", u, ok)
	}
	// Pull is allowed for any authenticated user; an unknown token is rejected.
	if _, ok := a.CheckBearer(ctx, "nope", "repository:acme/app:pull"); ok {
		t.Fatal("unknown token must be rejected")
	}
	// Write with no authorizer: authenticated ⇒ allowed (interim policy).
	if u, ok := a.CheckBearer(ctx, "good", "repository:acme/app:push"); !ok || u != "t-acme" {
		t.Fatalf("CheckBearer(good, push) = %q,%v", u, ok)
	}
	// Raw token + Basic (password carries the token) both validate.
	if u, ok := a.CheckToken(ctx, "good"); !ok || u != "t-acme" {
		t.Fatalf("CheckToken = %q,%v", u, ok)
	}
	if !a.CheckBasic(ctx, "ignored", "good") {
		t.Fatal("CheckBasic(good) should pass")
	}
	if a.CheckBasic(ctx, "ignored", "bad") {
		t.Fatal("CheckBasic(bad) should fail")
	}
}

func TestRemoteAuthAuthenticateForms(t *testing.T) {
	srv := fakeWhoami(map[string]string{"tok": "u1"})
	defer srv.Close()
	a := NewRemoteAuth(srv.URL)

	cases := []struct {
		name, auth, nuget string
		want              string
	}{
		{"bearer", "Bearer tok", "", "u1"},
		{"token-prefix", "token tok", "", "u1"},
		{"bare", "tok", "", "u1"},
		{"basic", "Basic " + base64.StdEncoding.EncodeToString([]byte("x:tok")), "", "u1"},
		{"nuget", "", "tok", "u1"},
		{"none", "", "", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.auth != "" {
			r.Header.Set("Authorization", c.auth)
		}
		if c.nuget != "" {
			r.Header.Set("X-NuGet-ApiKey", c.nuget)
		}
		if got := a.Authenticate(context.Background(), r); got != c.want {
			t.Errorf("%s: Authenticate = %q want %q", c.name, got, c.want)
		}
	}
}

func TestRemoteAuthNamespaceAuthorizer(t *testing.T) {
	srv := fakeWhoami(map[string]string{"good": "t-acme"})
	defer srv.Close()
	a := NewRemoteAuthWithAuthorizer(srv.URL, denyAllNS{})
	ctx := context.Background()

	// pull still allowed; push denied by the authorizer.
	if _, ok := a.CheckBearer(ctx, "good", "repository:acme/app:pull"); !ok {
		t.Fatal("pull should be allowed")
	}
	if _, ok := a.CheckBearer(ctx, "good", "repository:acme/app:push"); ok {
		t.Fatal("push should be denied by the authorizer")
	}
}

type denyAllNS struct{}

func (denyAllNS) CanWriteNS(context.Context, string, string) bool { return false }

func TestRemoteAuthIssueToken(t *testing.T) {
	a := NewRemoteAuth("http://127.0.0.1:1")
	ctx := context.Background()
	tok := a.IssueToken(ctx, "t-acme", []string{"repository:acme/app:push,pull"}, time.Minute)
	if tok == "" {
		t.Fatal("IssueToken returned empty")
	}
	// The minted bearer is recognized locally (no easyvcs round-trip needed).
	if u, ok := a.CheckBearer(ctx, tok, "repository:acme/app:push"); !ok || u != "t-acme" {
		t.Fatalf("minted push = %q,%v", u, ok)
	}
	if u, ok := a.CheckBearer(ctx, tok, "repository:acme/app:pull"); !ok || u != "t-acme" {
		t.Fatalf("minted pull = %q,%v", u, ok)
	}
}

func TestNamespaceOf(t *testing.T) {
	cases := map[string]string{
		"repository:acme/app:pull":   "acme",
		"repository:acme/app:push":   "acme",
		"repository:left-pad:pull":   "left-pad",
		"repository:@scope/pkg:pull": "@scope",
	}
	for in, want := range cases {
		if got := namespaceOf(in); got != want {
			t.Errorf("namespaceOf(%q) = %q want %q", in, got, want)
		}
	}
}
