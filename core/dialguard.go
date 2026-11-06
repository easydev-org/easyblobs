package easyblobkit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// SSRF guard for upstream fetches.
//
// Two layers, because neither alone is enough:
//
//  1. SYNTACTIC (targets.IsPublicHost): refuses a request-supplied host that is
//     not a plainly public DNS name. Cheap, but blind to a well-formed name that
//     RESOLVES to a private address ("127.0.0.1.nip.io").
//
//  2. TARGET (guardedTransport below): every outbound request — including each
//     hop of a redirect — has its destination checked after resolution. This is
//     where the real decision happens.
//
// A deployment that genuinely fetches from a private address (an internal
// mirror, an in-cluster upstream) opts in explicitly with
// EASYBLOBS_ALLOW_PRIVATE_UPSTREAM=1. Silence is not consent: the default
// refuses, because this code decides whether a request can reach the cluster it
// runs in.

// allowPrivateUpstreamEnv is the explicit opt-out of the target guard.
const allowPrivateUpstreamEnv = "EASYBLOBS_ALLOW_PRIVATE_UPSTREAM"

// privateNets are the ranges an upstream must never reach.
var privateNets = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",          // "this network"
		"10.0.0.0/8",         // RFC1918
		"100.64.0.0/10",      // CGNAT
		"127.0.0.0/8",        // loopback
		"169.254.0.0/16",     // link-local (cloud metadata)
		"172.16.0.0/12",      // RFC1918
		"192.0.0.0/24",       // IETF protocol assignments
		"192.0.2.0/24",       // TEST-NET-1
		"192.168.0.0/16",     // RFC1918
		"198.18.0.0/15",      // benchmarking
		"198.51.100.0/24",    // TEST-NET-2
		"203.0.113.0/24",     // TEST-NET-3
		"224.0.0.0/4",        // multicast
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // broadcast
		"::1/128",            // IPv6 loopback
		"::/128",             // IPv6 unspecified
		"fc00::/7",           // IPv6 ULA
		"fe80::/10",          // IPv6 link-local
		"ff00::/8",           // IPv6 multicast
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// IsPrivateAddr reports whether ip is one an upstream must not reach.
//
// Unspecified, loopback, link-local, private and multicast are all refused; a
// 4-in-6 mapped address is judged by its IPv4 form, since that is what it
// connects to.
func IsPrivateAddr(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsPrivate() {
		return true
	}
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// guardedTransport is an http.RoundTripper that refuses to send a request to a
// private destination.
//
// It sits at the ROUND TRIPPER layer, not at DialContext, for two reasons:
//
//   - it sees the REQUEST's host, so a deployment behind a private egress proxy
//     (the common in-cluster case) still has its real destination checked — a
//     DialContext check would instead inspect the PROXY address and either block
//     every deployment with an internal proxy or have to allow it;
//   - Go re-enters RoundTrip for EVERY redirect hop, so a public host that 302s
//     to 127.0.0.1 is refused on the second hop without extra code.
type guardedTransport struct {
	base http.RoundTripper
}

func (g *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := CheckUpstreamURL(req.Context(), req.URL.Host); err != nil {
		return nil, err
	}
	return g.base.RoundTrip(req)
}

// ErrPrivateUpstream is returned when a fetch would reach a private address.
var ErrPrivateUpstream = errors.New("refusing to reach a private address")

// CheckUpstreamHost validates a host:port (or bare host) an upstream fetch would
// contact. It is exported so callers that build their own transport (OCI's
// follow-redirect client) get the same policy.
func CheckUpstreamHost(ctx context.Context, hostport string) error {
	if allowPrivateUpstream(os.Getenv) {
		return nil
	}
	// A userinfo component ("user:pass@host") is NOT a host: the URL parser
	// would connect to whatever follows the '@'. Refuse it outright, before
	// resolution, so the message names the real problem.
	if strings.Contains(hostport, "@") {
		return fmt.Errorf("%w: %q contains userinfo; not a host", ErrPrivateUpstream, hostport)
	}
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return fmt.Errorf("%w: empty host", ErrPrivateUpstream)
	}
	// An IP literal is checked directly.
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateAddr(ip) {
			return fmt.Errorf("%w: %s is a private address (set %s=1 to allow an internal upstream)",
				ErrPrivateUpstream, host, allowPrivateUpstreamEnv)
		}
		return nil
	}
	// A name is resolved FIRST: "127.0.0.1.nip.io" is a valid public name.
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		// A name that does not resolve fails at dial anyway; report it as a
		// refusal so the message is the same either way.
		return fmt.Errorf("%w: %s did not resolve: %v", ErrPrivateUpstream, host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("%w: %s resolved to no address", ErrPrivateUpstream, host)
	}
	// EVERY answer must be public: a name returning one public and one private
	// address is a rebinding attempt, and picking "the public one" would leave
	// the race open.
	for _, a := range ips {
		if IsPrivateAddr(a.IP) {
			return fmt.Errorf("%w: %s resolves to %s (set %s=1 to allow an internal upstream)",
				ErrPrivateUpstream, host, a.IP, allowPrivateUpstreamEnv)
		}
	}
	return nil
}

// CheckUpstreamURL is CheckUpstreamHost for a URL's host component.
func CheckUpstreamURL(ctx context.Context, hostport string) error {
	return CheckUpstreamHost(ctx, hostport)
}

// allowPrivateUpstream reports whether the deployment opted out of the guard.
func allowPrivateUpstream(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(getenv(allowPrivateUpstreamEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// guardTransport wraps a transport with the target check.
func guardTransport(base http.RoundTripper) http.RoundTripper {
	return &guardedTransport{base: base}
}
