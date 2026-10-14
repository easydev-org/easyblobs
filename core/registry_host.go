package easyblobkit

import (
	"context"
	"strings"

	"github.com/easydev-org/easyblobs/targets"
)

// Registry host handling for the container protocols. A docker/podman client
// puts the registry in TLS SNI and the Host header, never in the path:
//
//	docker pull ghcr.io/acme/app  ->  Host: ghcr.io, GET /v2/acme/app/...
//	docker pull acme/app          ->  Host: registry-1.docker.io, GET /v2/acme/app/...
//
// So the Host is the only place the target registry can be learned, and it is
// also the right namespace: ghcr.io/acme/app and docker.io/acme/app are
// different images.

// canonicalHosts folds the aliases of one registry onto a single host name, so
// an image pulled by any alias lands in the same repository.
var canonicalHosts = map[string]string{
	"index.docker.io":      "docker.io",
	"registry-1.docker.io": "docker.io",
	"registry.docker.io":   "docker.io",
}

// CanonicalHost lower-cases a host and folds known registry aliases.
func CanonicalHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if c, ok := canonicalHosts[h]; ok {
		return c
	}
	return h
}

// IsRegistryHost reports whether a Host header names an upstream container
// registry rather than the gateway itself. The gateway is reached by an
// in-cluster name ("easy-platform", "easy-platform.ns.svc.cluster.local") or a bare
// address; a public registry is a dotted hostname. The docker
// insecure-registry convention additionally makes "localhost:port" and
// "IP:port" registries, which is what the test/local-registry paths use.
func IsRegistryHost(host string) bool {
	h := CanonicalHost(host)
	if h == "" {
		return false
	}
	hostOnly, _, hasPort := splitHostPort(h)
	switch {
	case hostOnly == "localhost" || isIPLiteral(hostOnly):
		// Bare addresses are the gateway; with an explicit port they follow
		// the insecure-registry convention and are treated as registries.
		return hasPort
	case strings.HasSuffix(hostOnly, ".local"),
		strings.HasSuffix(hostOnly, ".svc"),
		strings.HasSuffix(hostOnly, ".cluster.local"),
		strings.HasSuffix(hostOnly, ".internal"):
		return false
	}
	// A single-label name is an in-cluster service (easy-platform, artifact, ...).
	// Anything dotted is a public registry name.
	return strings.Contains(hostOnly, ".")
}

// splitHostPort splits "host:port"; a bare host returns hasPort=false.
func splitHostPort(h string) (host, port string, hasPort bool) {
	i := strings.LastIndexByte(h, ':')
	if i < 0 {
		return h, "", false
	}
	return h[:i], h[i+1:], true
}

func isIPLiteral(h string) bool {
	if h == "" {
		return false
	}
	dots, colons := 0, 0
	for _, c := range h {
		switch {
		case c >= '0' && c <= '9':
		case c == '.':
			dots++
		case c == ':':
			colons++
		case (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') || c == '%':
		default:
			return false
		}
	}
	return dots == 3 || colons > 0
}

type registryHostKey struct{}

// WithRegistryHost stores the registry host implied by a request's Host
// header. OCI uses it as the repository namespace and the upstream selector.
func WithRegistryHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, registryHostKey{}, host)
}

// RegistryHostFrom returns the registry host for a request: the explicit value
// from WithRegistryHost when set, else the Host the scope middleware recorded
// (OCI's /v2 mount). "" means "not a registry host", i.e. the default registry.
func RegistryHostFrom(ctx context.Context) string {
	h := RepoScopeFrom(ctx).Host
	if v, ok := ctx.Value(registryHostKey{}).(string); ok && v != "" {
		h = v
	}
	if !IsRegistryHost(h) {
		return ""
	}
	return h
}

// IsPublicHostname reports whether host looks like a public DNS name (dotted,
// not an IP literal, not a cluster-local name, not localhost). The git/ivy/
// netcache mirrors use it as the SSRF guard for a host named in the path: a
// host that fails it must never be used as an upstream source. It delegates to
// the shared targets guard so every layer agrees.
func IsPublicHostname(host string) bool {
	return targets.IsPublicHost(CanonicalHost(host))
}
