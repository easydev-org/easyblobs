package targets

import "testing"

// TestIsPublicHost is the regression suite for the SSRF guard.
//
// It exists because the guard had NO test at all, and three shapes that a
// suffix-check implementation accepted were confirmed to reach a loopback
// server in production:
//
//	evil.com:pass@127.0.0.1    the URL parser connects to 127.0.0.1
//	127.0.0.1.nip.io           wildcard DNS that resolves to loopback
//	foo.svc.cluster.local.     a trailing dot defeated the suffix match
//
// The table is the contract: a public DNS name is allowed, and EVERYTHING else
// is refused, because this value ends up in an outbound URL.
func TestIsPublicHost(t *testing.T) {
	blocked := []string{
		// Empty / whitespace / garbage.
		"", " ", "\t", "\n",
		// Loopback and unspecified, in the shapes a parser accepts.
		"localhost", "LOCALHOST", "localhost:8080", "localhost.",
		"127.0.0.1", "127.0.0.1:80", "127.1", "127.0.1", "0.0.0.0",
		"0", "0x7f.0.0.1", "0177.0.0.1", "2130706433", "127.0.0.1.",
		// RFC1918 / link-local / metadata.
		"10.0.0.1", "10.255.255.254", "172.16.0.1", "172.31.255.255",
		"192.168.0.1", "169.254.169.254", "169.254.0.1",
		// IPv6.
		"::1", "[::1]", "[::1]:80", "::", "[::]", "fe80::1",
		"[::ffff:127.0.0.1]", "[::ffff:10.0.0.1]", "fd00::1",
		// Cluster-local and service names.
		"foo.svc", "foo.SVC", "artifact", "easy-platform",
		"foo.cluster.local", "foo.cluster.local.", "a.b.cluster.local",
		"foo.internal", "foo.local", "local", "internal", "svc",
		// Trailing-dot bypass attempts on every cluster suffix.
		"x.svc.", "x.internal.", "x.local.",
		// Userinfo: the URL parser would connect to the part after '@'.
		"evil.com:pass@127.0.0.1",
		"evil.com@127.0.0.1",
		"user:pw@evil.com",
		"@evil.com",
		"evil.com#@127.0.0.1",
		// Wildcard DNS pointing at a private address (syntactically public, so
		// the RESOLVER guard is what stops it -- but the '127.0.0.1' label makes
		// this particular one worth pinning as NOT a plain public host? No: it
		// resolves via DNS, so the name itself is public. Covered in core.)
		// Not valid DNS at all.
		"a..b", "..", ".", "-", "a-.b", "-a.b", "a.-b",
		"under_score.com", "foo:bar", "://",
		"nodots",
	}

	for _, h := range blocked {
		if IsPublicHost(h) {
			t.Errorf("IsPublicHost(%q) = true, want false (SSRF)", h)
		}
	}

	allowed := []string{
		"registry.npmjs.org", "pypi.org", "github.com", "crates.io",
		"repo.maven.apache.org", "registry-1.docker.io", "ghcr.io",
		"d1abc.cloudfront.net", "cdn.jsdelivr.net",
		"EXAMPLE.COM", "Example.Com", "x.y",
		"a-b.c-d.example",
		"host.with-hyphens.example.org",
		// A dotted name with a numeric label that is not the TLD.
		"1.2.3.4.example.com",
	}
	for _, h := range allowed {
		if !IsPublicHost(h) {
			t.Errorf("IsPublicHost(%q) = false, want true", h)
		}
	}
}

// TestIsPublicHostRejectsUserinfoSpecifically documents the exact bug: the
// string is a valid public name UNTIL the '@', and the URL parser then connects
// to what follows it.
func TestIsPublicHostRejectsUserinfoSpecifically(t *testing.T) {
	host := "evil.com:pass@127.0.0.1"
	if IsPublicHost(host) {
		t.Fatalf("IsPublicHost(%q) must be false: the URL parser resolves it to 127.0.0.1", host)
	}
}

// TestIsPublicHostTrailingDot pins the ".cluster.local." bypass.
func TestIsPublicHostTrailingDot(t *testing.T) {
	for _, h := range []string{"x.svc.", "x.cluster.local.", "x.internal.", "x.local."} {
		if IsPublicHost(h) {
			t.Errorf("IsPublicHost(%q) = true; a trailing dot must not defeat the suffix check", h)
		}
	}
}

// TestIsPublicHostNumericTLD pins the alternate-IPv4 spellings.
func TestIsPublicHostNumericTLD(t *testing.T) {
	for _, h := range []string{"127.1", "0x7f.0.0.1", "0177.0.0.1", "1.2.3.4"} {
		if IsPublicHost(h) {
			t.Errorf("IsPublicHost(%q) = true; a numeric TLD is an address spelling, not a hostname", h)
		}
	}
}
