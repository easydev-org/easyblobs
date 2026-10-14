package easyblobkit

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/targets"
)

// TargetDispatcher routes /artifacts/<target-id>/... to the adapter for the
// target's protocol, resolving the target at REQUEST time. It replaces the
// startup-time per-target mux registrations, so a target created through the
// admin API is reachable immediately (no restart) and built-ins, persisted and
// default targets all flow through one code path.
//
// The adapter never sees the target: the path is rewritten to the protocol
// mount (/artifacts/<protocol>/<rest>) and the target identity is carried in
// the request scope, where it selects the upstream and, for an isolated
// target, the storage prefix.
type TargetDispatcher struct {
	Registry *Registry
	// Handlers maps a protocol name to its adapter. Built once at startup.
	Handlers map[string]http.Handler
	// Base is the mount prefix (normally MountBase).
	Base string
}

// NewTargetDispatcher builds the dispatcher. Handlers are keyed by protocol.
func NewTargetDispatcher(base string, reg *Registry, handlers map[string]http.Handler) *TargetDispatcher {
	return &TargetDispatcher{Registry: reg, Handlers: handlers, Base: base}
}

// ServeHTTP resolves the first path segment as a protocol mount (a protocol's
// default target). A segment that is not a mounted protocol falls through to
// the protocol of the same name when an adapter exists (legacy behavior; the
// OCI /v2 path is mounted separately). A named target is NOT a mount — use
// /artifacts/<proto>/_upstream/<host>/... instead.
func (d *TargetDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, d.Base)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	id, tail, hasTail := splitFirstSegment(rest)

	reg := d.targetRegistry()
	var tgt targets.Target
	var ok bool
	if reg != nil {
		// Only a protocol's DEFAULT target (ID == Protocol) is a mount. A NAMED
		// target (e.g. maven.google) is reached via
		// /artifacts/<proto>/_upstream/<host>/..., not a mount of its own.
		if t, found := reg.Get(id); found && t.ID == t.Protocol {
			tgt, ok = t, true
		}
	}
	if !ok {
		// Not a known target id: treat the segment as a bare protocol (the
		// historical shape, kept so a request to an unmounted protocol still
		// routes when the adapter exists).
		tgt, ok = d.defaultTargetFor(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
	}

	h := d.Handlers[tgt.Protocol]
	if h == nil {
		http.NotFound(w, r)
		return
	}

	// Rewrite to the protocol mount and build the scope.
	protoPath := d.Base + "/" + tgt.Protocol
	if tail != "" || hasTail {
		protoPath += "/" + tail
	}
	rr := r.Clone(r.Context())
	u := *r.URL
	u.Path = protoPath
	if u.RawPath != "" {
		u.RawPath = ""
	}
	rr.URL = &u

	scope, finalPath, sok := resolveScope(d.Base, rr)
	if sok {
		scope.Target = tgt.ID
		scope.TargetShared = tgt.Shared()
		rr.URL.Path = finalPath
		rr = rr.WithContext(WithRepoScope(rr.Context(), scope))
	}
	h.ServeHTTP(w, rr)
}

// targetRegistry returns the configured target registry, or nil.
func (d *TargetDispatcher) targetRegistry() *targets.Registry {
	if d.Registry == nil || d.Registry.Upstreams == nil {
		return nil
	}
	return d.Registry.Upstreams.Targets
}

// defaultTargetFor synthesizes the default target for a protocol name.
func (d *TargetDispatcher) defaultTargetFor(proto string) (targets.Target, bool) {
	if _, ok := d.Handlers[proto]; !ok {
		return targets.Target{}, false
	}
	if reg := d.targetRegistry(); reg != nil {
		if t, ok := reg.Default(proto); ok {
			return t, true
		}
	}
	return targets.Target{ID: proto, Protocol: proto, Kind: targets.Mirror}, true
}

// splitFirstSegment splits "a/b/c" into ("a", "b/c", true); "a" into
// ("a", "", false).
func splitFirstSegment(p string) (first, rest string, hasRest bool) {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:], true
	}
	return p, "", false
}
