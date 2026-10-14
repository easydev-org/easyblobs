package easyblobkit

import (
	"context"
	"fmt"

	"github.com/easydev-org/easyblobs/targets"
)

// UpstreamSpec selects which upstream a Remote targets. Exactly one selector
// is used, in this precedence:
//
//	Base  an absolute base URL, verbatim (no target auth; generic proxy)
//	Host  address the upstream by hostname (git/ivy source mirrors)
//	Sub   a format sub-endpoint on its own host (cargo index, nuget search)
//	Repo  one repository of Format (per-repo override + target auth)
//	zero  the request-context priority for Format (override -> client origin
//	      -> table default), applying the resolved target's auth
//
// It is the ONE constructor for upstream handles; adapters never assemble a
// base themselves.
type UpstreamSpec struct {
	Format string
	Repo   string
	Sub    string
	Host   string
	Base   string
}

// Remote resolves an upstream handle for a spec. A Base is honored verbatim; a
// Host addresses the source by hostname; a Sub targets a sub-endpoint; else the
// format/repository is resolved from the request context (per-repo override ->
// client origin -> table default) with the target's auth applied.
func (r *Registry) Remote(ctx context.Context, spec UpstreamSpec) (*Remote, error) {
	switch {
	case spec.Base != "":
		return r.remoteAtBase(spec.Base), nil
	case spec.Host != "":
		if m, ok := r.remoteHost(spec.Format, spec.Host); ok {
			return m, nil
		}
		return nil, fmt.Errorf("no upstream for host %s", spec.Host)
	case spec.Sub != "":
		return r.remoteSub(ctx, spec.Format, spec.Sub)
	default:
		repo := spec.Repo
		if repo == "" {
			sc := RepoScopeFrom(ctx)
			repo = sc.Namespace
			if repo == "" {
				repo = sc.Name
			}
		}
		return r.remoteFor(ctx, spec.Format, repo, "")
	}
}

// proxyPtr returns a *string to the configured proxy for key, or nil when
// unset (meaning "follow environment proxy"). An explicitly configured empty
// string means "direct" and produces a pointer to "".
func proxyPtr(u *Upstreams, key string) *string {
	v, ok := u.ProxyURL(key)
	if !ok {
		return nil
	}
	return &v
}

// resolveBase applies the upstream priority above and returns the base plus
// its proxy policy. ok is false when no upstream is configured (air-gap).
func (r *Registry) resolveBase(ctx context.Context, format, repo string) (string, *string, bool) {
	u := r.Upstreams
	if u == nil || u.AirGap {
		return "", nil, false
	}
	// 1. explicit per-repository override.
	if e, ok := u.RepoOverride(format, repo); ok {
		var pp *string
		if e.Proxy != "" {
			pp = &e.Proxy
		} else if p, has := u.ProxyURL(format); has {
			pp = &p
		}
		return e.Base, pp, true
	}
	// 1b. an explicitly mounted target (/artifacts/<target>/...) is operator
	// intent and beats the host the client happened to dial: a request to a
	// mirror's own path always means that mirror.
	// 1a. an explicit "/_upstream/<host>/" segment (per-request intent) selects
	// the upstream by host: a declared target keeps its base path + auth; any
	// other public host is used as https://<host>. It beats the mounted target
	// and the table default, but not a per-repository override (server config).
	if host := RepoScopeFrom(ctx).Upstream; host != "" {
		var pp *string
		if p, has := u.ProxyURL(format); has {
			pp = &p
		}
		if u.Targets != nil {
			if t, ok := u.Targets.ForHostProtocol(format, host); ok && !t.HostOnly() && t.Base != "" {
				return trimSlash(t.Base), pp, true
			}
		}
		if targets.IsPublicHost(host) {
			return "https://" + host, pp, true
		}
		return "", nil, false
	}
	if t, ok := u.targetFromScope(ctx); ok && !t.HostOnly() {
		var pp *string
		if t.Proxy != "" {
			pp = &t.Proxy
		} else if p, has := u.ProxyURL(format); has {
			pp = &p
		}
		return trimSlash(t.Base), pp, true
	}
	// 2. table default (an explicitly mounted target beats it, above).
	if base := u.Repo(format, repo); base != "" {
		proxy, has := u.RepoProxy(format, repo)
		var pp *string
		if has {
			pp = &proxy
		}
		return base, pp, true
	}
	return "", nil, false
}

// remoteFor resolves a Remote for one repository using resolveBase, applying
// the resolved target's auth policy (passthrough/basic/bearer).
func (r *Registry) remoteFor(ctx context.Context, format, repo, upstreamBase string) (*Remote, error) {
	if upstreamBase != "" {
		// An explicit base is honored verbatim but keeps the repository's
		// proxy policy (the original RemoteFor semantics).
		proxy, has := r.Upstreams.RepoProxy(format, repo)
		var pp *string
		if has {
			pp = &proxy
		}
		return NewRemote(sharedFactory, upstreamBase, pp), nil
	}
	base, proxy, ok := r.resolveBase(ctx, format, repo)
	if !ok {
		return nil, fmt.Errorf("no upstream for %s/%s", format, repo)
	}
	factory := sharedFactory
	remote := NewRemote(factory, base, proxy)
	return r.applyAuth(ctx, format, repo, remote)
}

// applyAuth layers the resolved target's auth onto a remote. When no target is
// configured the remote is returned unchanged (anonymous).
func (r *Registry) applyAuth(ctx context.Context, format, repo string, remote *Remote) (*Remote, error) {
	t, ok := r.targetFor(ctx, format, repo)
	if !ok {
		return remote, nil
	}
	clientAuth := RepoScopeFrom(ctx).ClientAuth
	authed, err := withTargetAuth(remote, t, clientAuth)
	if err != nil {
		return nil, fmt.Errorf("auth for target %s: %w", t.ID, err)
	}
	return authed, nil
}

// targetFor resolves the target a (format, repo) request maps to: the explicit
// scope target, else the protocol default.
func (r *Registry) targetFor(ctx context.Context, format, repo string) (targets.Target, bool) {
	u := r.Upstreams
	if u == nil || u.Targets == nil {
		return targets.Target{}, false
	}
	if t, ok := u.targetFromScope(ctx); ok {
		return t, true
	}
	return u.Targets.Default(format)
}

// remoteAtBase is the ctx-free absolute-base constructor.
func (r *Registry) remoteAtBase(base string) *Remote {
	factory := sharedFactory
	return NewRemote(factory, base, proxyPtr(r.Upstreams, "generic"))
}

// remoteSub resolves a sub-endpoint of a format (cargo's index/static, nuget's
// search/registration, hex's api, ...). The configured sub-endpoint base is
// authoritative: a sub-endpoint lives on a host of its own. A per-repository
// override still wins, and the format default is the last resort.
func (r *Registry) remoteSub(ctx context.Context, format, sub string) (*Remote, error) {
	u := r.Upstreams
	if u == nil {
		return nil, fmt.Errorf("no upstream for %s.%s", format, sub)
	}
	if base := u.Sub(format, sub); base != "" {
		return r.remoteAtBase(base), nil
	}
	if base := u.Get(format); base != "" {
		return r.remoteAtBase(base), nil
	}
	return nil, fmt.Errorf("no upstream for %s.%s", format, sub)
}

// remoteHost resolves a Remote for an upstream identified by its hostname
// (the git/ivy mirrors address servers directly). Priority: a per-host
// repository override, then a syntactically valid public host (source mirrors
// treat the requested host as the source identity).
func (r *Registry) remoteHost(format, host string) (*Remote, bool) {
	if r.Upstreams == nil {
		return nil, false
	}
	if e, ok := r.Upstreams.RepoOverride(format, host); ok {
		return r.remoteAtBase(e.Base), true
	}
	if !IsPublicHostname(host) {
		return nil, false
	}
	return r.remoteAtBase("https://" + host), true
}
