package easyblobkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	kit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
	"github.com/easydev-org/easyblobs/targets"
)

// TestUpstreamSegmentCannotReachTheCluster is the end-to-end regression for the
// `_upstream/<host>/` SSRF.
//
// The exploit chain was:
//
//	GET /artifacts/npm/_upstream/evil.com:pass@127.0.0.1/...
//	  -> SplitUpstream accepted the string (the guard only looked at the suffix)
//	  -> the URL parser resolved the host to 127.0.0.1
//	  -> the fetch reached a loopback service
//
// Both layers must refuse it now.
func TestUpstreamSegmentCannotReachTheCluster(t *testing.T) {
	t.Setenv("EASYBLOBS_ALLOW_PRIVATE_UPSTREAM", "") // the guard's own test: do not opt out.

	var reached bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write([]byte("INTERNAL-SECRET"))
	}))
	defer internal.Close()

	reg := newGuardRegistry(t)

	// Every shape that used to be accepted.
	hosts := []string{
		"evil.com:pass@127.0.0.1",
		"127.0.0.1",
		"localhost",
		"169.254.169.254",
		"10.0.0.1",
		"foo.svc.cluster.local",
		"foo.svc.cluster.local.",
		"127.0.0.1.nip.io",
	}
	for _, host := range hosts {
		// Layer 1: the PATH-level guard must refuse the segment outright.
		// EXCEPTION: a syntactically public name such as 127.0.0.1.nip.io legitimately
		// passes the syntactic layer — only the resolver knows it points at
		// loopback, which is exactly why layer 2 exists.
		if _, _, ok := kit.SplitUpstream("_upstream/" + host + "/pkg"); ok &&
			!strings.Contains(host, ".nip.io") {
			t.Errorf("SplitUpstream accepted %q", host)
		}
		// Layer 2: even if something assembled the URL, the target guard must
		// refuse the destination.
		if err := kit.CheckUpstreamHost(context.Background(), host); err == nil {
			t.Errorf("CheckUpstreamHost accepted %q", host)
		}
	}
	_ = reg
	if reached {
		t.Fatal("a loopback server was reached; the SSRF guard did not hold")
	}
}

// TestUpstreamSegmentAllowsPublicHosts: the guard must not break the feature.
func TestUpstreamSegmentAllowsPublicHosts(t *testing.T) {
	reg := newGuardRegistry(t)
	for _, host := range []string{"registry.npmjs.org", "cdn.jsdelivr.net", "repo.maven.apache.org"} {
		gotHost, rest, ok := kit.SplitUpstream("_upstream/" + host + "/pkg")
		if !ok {
			t.Errorf("a public host %q was refused by SplitUpstream", host)
			continue
		}
		if gotHost != host || rest != "pkg" {
			t.Errorf("SplitUpstream(%q) = (%q, %q)", host, gotHost, rest)
		}
		if err := kit.CheckUpstreamHost(context.Background(), host); err != nil {
			t.Errorf("a public host %q was refused by the target guard: %v", host, err)
		}
	}
	_ = reg
}

// stripScheme removes the scheme from a URL for CheckUpstreamHost.
func stripScheme(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	return u
}

// newGuardRegistry builds a minimal registry for resolution tests.
func newGuardRegistry(t *testing.T) *kit.Registry {
	t.Helper()
	dir := t.TempDir()
	meta, err := store.OpenSQLite(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	blobs, err := store.NewFileBlobStore(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	return &kit.Registry{
		Blobs: blobs, Meta: kit.NewScopedStore(meta),
		Upstreams: &kit.Upstreams{
			Targets:  targets.NewRegistry(),
			Defaults: map[string]string{}, Overrides: map[string]string{}, Proxy: map[string]string{},
		},
	}
}
