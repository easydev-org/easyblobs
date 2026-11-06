// Package targets is the single source of truth for package-mirror targets.
//
// A *protocol* is the wire shape (maven's <group>/<artifact>/<version>/<file>,
// npm's packument, OCI's /v2 manifest flow). A *target* is where a protocol's
// content comes from: a base URL and the hostnames a client may address it by.
//
// The package is deliberately stdlib-only. It is imported by the artifact
// server and by easy-platform; the latter must never pull gorm/sqlite in, so this
// module carries no dependencies.
//
// Layering:
//
//	protocol : wire semantics, selected by target.Protocol
//	target   : upstream identity + routing (this package)
//	storage  : Keyed by (protocol, repository, version). A mirror target
//	           shares the protocol's namespace (same coordinates dedupe by
//	           digest); an identity target's host is part of the repository.
package targets

import (
	"net"
	"strings"
)

// MountBase is the HTTP prefix every non-OCI protocol is mounted under.
// OCI is spec-fixed at /v2 and is not mounted here.
const MountBase = "/artifacts"

// Kind distinguishes how a target's host participates in content identity.
type Kind string

const (
	// Mirror serves content for an existing protocol whose coordinates are
	// already global (maven coordinates, npm package names). Several mirrors
	// of the same protocol share storage: the same (repo, version) pulled
	// through Central and Google Maven is one row and one CAS blob.
	Mirror Kind = "mirror"
	// Identity means the upstream host IS part of the content's identity:
	// ghcr.io/acme/app and docker.io/acme/app are different images, and so are
	// the same repo path on two different git hosts. The host becomes the
	// repository namespace.
	Identity Kind = "identity"
)

// AuthMode selects how a target authenticates to its upstream.
type AuthMode string

const (
	// AuthPassthrough forwards the client's own credentials to the upstream
	// (a private repository the client is already authorized for).
	AuthPassthrough AuthMode = "passthrough"
	// AuthBasic uses a stored username/password for the upstream.
	AuthBasic AuthMode = "basic"
	// AuthBearer uses a stored token, acquired or refreshed out of band.
	AuthBearer AuthMode = "bearer"
)

// Auth is a target's upstream credential policy. Secret is a reference
// (e.g. an env var name or secret key), never the credential itself.
type Auth struct {
	Mode     AuthMode `json:"mode,omitempty"`
	Username string   `json:"username,omitempty"`
	Secret   string   `json:"secret,omitempty"`
}

// Target is one upstream a protocol can pull from.
type Target struct {
	// ID is the stable target key, and the path segment under MountBase. The
	// protocol's default target reuses the protocol name ("maven"); a named
	// mirror is "<protocol>.<name>" ("maven.google").
	ID string `json:"id"`
	// Protocol selects the adapter. Always the leading component of ID for
	// named targets, but stored explicitly so a target can be re-pointed.
	Protocol string `json:"protocol"`
	// Kind decides storage sharing and whether Hosts are identity.
	Kind Kind `json:"kind"`
	// Base is the upstream root (scheme://host[/path]). Empty for an Identity
	// target whose base is derived from the request host (git, OCI).
	Base string `json:"base"`
	// Hosts are the hostname patterns a client may address this target by:
	// the upstream's own host plus any alternate mirrors of the same content.
	// They drive the SSRF allow-list. Exact names, "*.suffix" wildcards, or
	// bare suffixes.
	Hosts []string `json:"hosts,omitempty"`
	// Aux maps a sub-endpoint name to its own base URL, for endpoints that
	// live on a different host than the primary upstream (crates.io's index
	// and static downloads). Aux entries are never mounted, but their hosts
	// are part of the SSRF allow-list.
	Aux map[string]string `json:"aux,omitempty"`
	// Proxy is a per-target proxy policy; "" means derive from the protocol.
	Proxy string `json:"proxy,omitempty"`
	// Share overrides Kind's default storage sharing. Nil for built-ins
	// (Kind decides); user-declared targets default to isolated.
	Share *bool `json:"share,omitempty"`
	// Auth is the upstream credential policy (empty = anonymous).
	Auth Auth `json:"auth,omitempty"`
	// Builtin marks a target shipped in the binary (not user-declared).
	Builtin bool `json:"builtin,omitempty"`
}

// Shared reports whether this target's content is stored in the protocol's
// shared namespace (true) or isolated under the target (false).
func (t Target) Shared() bool {
	if t.Share != nil {
		return *t.Share
	}
	return t.Kind != Identity
}

// Mount is the path prefix this target is served under: /artifacts/<ID>.
func (t Target) Mount() string { return MountBase + "/" + t.ID }

// HostOnly reports whether the target has no fixed base (its upstream is the
// request host itself), which is what makes git/OCI targets dynamic.
func (t Target) HostOnly() bool { return t.Base == "" }

// SplitID returns the protocol prefix and the remaining name of a dotted
// target id: "maven.google" -> ("maven", "google"), "maven" -> ("maven", "").
func SplitID(id string) (proto, name string) {
	if i := strings.IndexByte(id, '.'); i >= 0 {
		return id[:i], id[i+1:]
	}
	return id, ""
}

// IsPublicHost reports whether host is a public DNS name: dotted, not an IP
// literal, not localhost, not a cluster-local name. It is the SSRF guard for a
// host named in the REQUEST, which must never become an upstream source unless
// it is a plainly public name.
//
// It is a SYNTACTIC guard and deliberately strict: anything it does not
// POSITIVELY recognise as a public DNS name is refused. That is the only safe
// default for a value that ends up in an outbound URL — an allow-list of bad
// shapes can never be complete.
//
// Shapes it refuses, each of which a looser check (a suffix test on the raw
// string) let through in production:
//
//	evil.com:pass@127.0.0.1   userinfo: the URL parser connects to 127.0.0.1
//	127.0.0.1.nip.io          a wildcard DNS name that RESOLVES to loopback
//	foo.svc.cluster.local.    a trailing dot defeated the ".cluster.local" suffix
//	0x7f.0.0.1, 127.1, 0177.0.0.1   alternate IPv4 spellings
//	a..b, ., .., -        not valid DNS at all
//
// Note that a syntactic check cannot catch a wildcard DNS name that resolves to
// a private address; that is the resolver's job (see core's dial guard), and
// the two together are what make the guard meaningful.
func IsPublicHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	// Reject userinfo outright: "user:pass@host" is not a host, and passing it
	// through means whoever parses it next decides what to connect to.
	if strings.Contains(h, "@") {
		return false
	}
	if hostOnly, _, err := net.SplitHostPort(h); err == nil {
		h = hostOnly
	}
	h = strings.Trim(h, "[]")
	// A trailing dot is a valid FQDN root marker, and "svc.cluster.local." must
	// not slip past the suffix checks below.
	h = strings.TrimSuffix(h, ".")
	if h == "" || h == "localhost" {
		return false
	}
	if ip := net.ParseIP(h); ip != nil {
		return false
	}
	switch {
	case h == "local", h == "internal", h == "svc", h == "cluster":
		return false
	case strings.HasSuffix(h, ".local"),
		strings.HasSuffix(h, ".svc"),
		strings.HasSuffix(h, ".cluster.local"),
		strings.HasSuffix(h, ".internal"):
		return false
	}
	// Must be a syntactically valid dotted DNS name. "a..b", ".", "127.1" and
	// the hex/octal IPv4 spellings all fail this, which is why the rules below
	// are about LABELS rather than about "contains a dot".
	return isDNSName(h)
}

// isDNSName reports whether h is a dotted name whose labels are all valid
// (letters, digits, hyphen; not starting or ending with a hyphen; 1..63 long;
// the last label alphabetic). It refuses numeric-looking TLDs so "127.1",
// "1.2.3.4" and "0x7f.0.0.1" are not mistaken for hostnames.
func isDNSName(h string) bool {
	if len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	labels := strings.Split(h, ".")
	for i, l := range labels {
		if l == "" || len(l) > 63 {
			return false
		}
		if l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for j := 0; j < len(l); j++ {
			c := l[j]
			ok := c == '-' || (c >= '0' && c <= '9') ||
				(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
			if !ok {
				return false
			}
		}
		// The TLD must not be all-numeric: "1.2.3.4" / "127.1" are addresses.
		if i == len(labels)-1 && isAllDigits(l) {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
