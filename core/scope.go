package easyblobkit

import (
	"context"
	"strings"
)

// RepoScope is the repository a request targets, resolved once at the mount
// boundary. It is carried in the request context so the storage and upstream
// layers can isolate content and choose a remote WITHOUT every adapter
// threading the namespace through its calls.
//
// Namespace resolution order:
//  1. an explicit "/-/<repo>/" marker in the path, else
//  2. the format's NamespaceResolver (npm scope, maven groupId, go module
//     prefix, apk/debian/rpm/conda first segment, OCI registry host), else
//  3. DefaultNamespace.
//
// Host is the request's Host header. It matters for OCI: a docker client puts
// the registry in SNI/Host, never in the path, so the host is the only place
// the registry can be learned (and it is the repository namespace). The OCI
// resolver reads it from here. For every other protocol the upstream comes from
// the target table or an explicit override, never from this Host.
//
// Proto is the scheme the client used (r.TLS), used to emit self-URLs.
type RepoScope struct {
	Format    string
	Namespace string
	Name      string
	Host      string
	Proto     string
	// Explicit is true when the request used the "/-/<repo>/" marker.
	Explicit bool
	// Hosted is true when the request addressed a self-published repository
	// through the reserved "_hosted/<repo>/" segment. The adapter routes on it
	// (hosted XOR proxy) instead of probing. Namespace carries the repo key in
	// that case, so the storage key ("hosted/<repo>" under the namespace) is
	// unchanged.
	Hosted bool
	// Target is the target id the request was routed through ("maven",
	// "maven.google"). Empty means the protocol's default target.
	Target string
	// Upstream is the host named by an explicit "/_upstream/<host>/" segment.
	// It overrides the protocol's default upstream for this request (resolved
	// against the target table, so a declared host keeps its base path and auth;
	// an ad-hoc public host is used as https://<host>). Empty means no explicit
	// upstream.
	Upstream string
	// TargetShared is true when the target stores into the protocol's shared
	// namespace (mirrors) rather than an isolated one (user-declared private
	// repositories). Only meaningful when Target is set.
	TargetShared bool
	// ClientAuth is the request's Authorization header, captured so a target
	// with passthrough auth can forward the caller's own credential upstream.
	// It is never logged or stored as key material.
	ClientAuth string
}

// RepoKey encodes (namespace, name) into the canonical repository string. The
// default namespace is elided so unscoped names keep their historical key (and
// existing metadata rows keep resolving). It is the primitive behind
// RepoScope.ScopedKey, which adds the isolated-target prefix on top.
func RepoKey(namespace, name string) string {
	namespace = strings.Trim(namespace, "/")
	name = strings.Trim(name, "/")
	if namespace == "" || namespace == DefaultNamespace {
		return name
	}
	if name == "" {
		return namespace
	}
	return namespace + "/" + name
}

type repoScopeKey struct{}

// WithRepoScope returns a context carrying the request's repository scope.
func WithRepoScope(ctx context.Context, s RepoScope) context.Context {
	return context.WithValue(ctx, repoScopeKey{}, s)
}

// RepoScopeFrom returns the request's repository scope, or the zero value.
func RepoScopeFrom(ctx context.Context) RepoScope {
	s, _ := ctx.Value(repoScopeKey{}).(RepoScope)
	return s
}

// ScopedName maps an adapter-computed repository key onto its fully-qualified
// form for the scope. The namespace is prefixed only when the key does not
// already carry it, so npm's "@scope/name" (where the adapter already includes
// the scope) is left alone while maven's bare artifactId and a flat protocol's
// package name gain the namespace.
func (s RepoScope) ScopedName(key string) string {
	ns := strings.Trim(s.Namespace, "/")
	if ns == "" || ns == DefaultNamespace {
		return key
	}
	// Idempotent: a key that already carries its namespace (npm's
	// "@scope/name", conda's "channel/subdir") is left unchanged, so adapters
	// that encode the namespace themselves are not double-prefixed.
	if key == ns || strings.HasPrefix(key, ns+"/") {
		return key
	}
	return RepoKey(ns, key)
}

// targetPrefix is the storage key prefix that isolates a target's content from
// the protocol's shared namespace: "" for a shared (mirror) target or the
// default target, "t:<id>" for an isolated (user-declared private) one.
func (s RepoScope) targetPrefix() string {
	if s.Target == "" || s.TargetShared {
		return ""
	}
	return "t:" + s.Target
}

// ScopedKey is the canonical storage key for an adapter-supplied repository:
// the target prefix (when isolated) around the namespace-qualified name.
func (s RepoScope) ScopedKey(key string) string {
	key = s.ScopedName(key)
	if p := s.targetPrefix(); p != "" {
		if key == p || strings.HasPrefix(key, p+"/") {
			return key
		}
		return p + "/" + key
	}
	return key
}

// UnscopedKey is ScopedKey's inverse for metadata reads/list filtering: it
// strips the target prefix first, then the namespace.
func (s RepoScope) UnscopedKey(key string) string {
	if p := s.targetPrefix(); p != "" {
		if key == p {
			return ""
		}
		if strings.HasPrefix(key, p+"/") {
			key = key[len(p)+1:]
		}
	}
	return s.UnscopedName(key)
}

// UnscopedName is ScopedName's inverse for catalog/list filtering.
func (s RepoScope) UnscopedName(key string) string {
	ns := strings.Trim(s.Namespace, "/")
	if ns == "" || ns == DefaultNamespace {
		return key
	}
	if key == ns {
		return ""
	}
	if strings.HasPrefix(key, ns+"/") {
		return key[len(ns)+1:]
	}
	return key
}
