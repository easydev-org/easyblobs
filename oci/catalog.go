package oci

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// imageEntry is one owner-scoped image with its real tags (no digest rows).
type imageEntry struct {
	Owner string   `json:"owner"`
	Name  string   `json:"name"`
	Tags  []string `json:"tags"`
}

// catalog serves the registry's image catalog. It is the OCI answer to a
// gateway's "list the images for an owner" query (the Forgejo
// ListContainerPackages replacement).
//
//	GET /v2/_catalog                       -> {"repositories":["acme/app", ...]}
//	GET /v2/_catalog?owner=<org>           -> {"images":[{"owner","name","tags"}, ...]}
//	GET /v2/_catalog?owner=<org>&name=<img> -> {"owner","name","tags"}
//
// Storage form: a repository key is "<namespace>/<name>", where the namespace
// is either the request Host (a deployment detail, used when the client pushed
// without an explicit registry prefix) or an explicit "<host>" the client named
// (e.g. docker.io/library/alpine). The catalog strips the DEPLOYMENT's own host
// prefix so callers see stable names ("acme/app"), and answers owner queries
// whether or not that prefix is present.
//
// Auth: the catalog enumerates every namespace, so it is gated whenever auth is
// configured — ANY valid credential (read or write) is accepted. Pull
// (manifests/blobs) stays anonymous; only this browsing endpoint is gated.
func (a *Adapter) catalog(w http.ResponseWriter, r *http.Request) {
	if a.state.Auth != nil && a.state.Auth.Authenticate(r.Context(), r) == "" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="oci-registry"`, a.tokenRealm()))
		writeJSON(w, http.StatusUnauthorized, ociError("UNAUTHORIZED", "authentication required"))
		return
	}
	repos, _ := a.state.Registry.Meta.ListRepositoriesByFormat(r.Context(), "oci")
	own := a.ownHosts(r)

	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	if owner == "" {
		// Flat repository list: strip the deployment host, dedupe, sort.
		set := map[string]bool{}
		for _, key := range repos {
			if v := stripOwnHost(key, own); v != "" {
				set[v] = true
			}
		}
		out := make([]string, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		sort.Strings(out)
		writeJSON(w, http.StatusOK, map[string]any{"repositories": out})
		return
	}

	// All images under the owner, matched on the host-stripped key.
	names := map[string]bool{}
	for _, key := range repos {
		if o, n, ok := ownerName(stripOwnHost(key, own)); ok && o == owner {
			names[n] = true
		}
	}

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		if !names[name] {
			writeJSON(w, http.StatusNotFound, ociError("NAME_UNKNOWN", "repository name not known to registry"))
			return
		}
		writeJSON(w, http.StatusOK, imageEntry{Owner: owner, Name: name, Tags: a.tagsFor(r, repos, own, owner, name)})
		return
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	images := make([]imageEntry, 0, len(sorted))
	for _, n := range sorted {
		images = append(images, imageEntry{Owner: owner, Name: n, Tags: a.tagsFor(r, repos, own, owner, n)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": images})
}

// tagsFor returns the real tags (digest rows excluded) for owner/name, looked
// up via the full stored repository key.
func (a *Adapter) tagsFor(r *http.Request, repos, own []string, owner, name string) []string {
	key, ok := a.repoKeyFor(repos, own, owner, name)
	if !ok {
		return []string{}
	}
	// The catalog is cross-namespace: read the repository key verbatim, without
	// the request's scope re-qualifying it.
	versions, _ := a.state.Registry.Meta.ListVersions(unscoped(r.Context()), "oci", key)
	tags := make([]string, 0, len(versions))
	for _, v := range versions {
		if strings.HasPrefix(v, "sha256:") {
			continue // digest alias row, not a tag
		}
		tags = append(tags, v)
	}
	sort.Strings(tags)
	return tags
}

// repoKeyFor finds the full stored key for owner/name.
func (a *Adapter) repoKeyFor(repos, own []string, owner, name string) (string, bool) {
	for _, key := range repos {
		if o, n, ok := ownerName(stripOwnHost(key, own)); ok && o == owner && n == name {
			return key, true
		}
	}
	return "", false
}

// ownHosts lists the hosts that belong to THIS deployment (the request Host
// when it is a registry host, and the configured self-base host). A repository
// key starting with one of them carries a deployment-internal namespace prefix
// that must not leak into catalog output.
func (a *Adapter) ownHosts(r *http.Request) []string {
	var out []string
	add := func(h string) {
		h = easyblobkit.CanonicalHost(strings.TrimSpace(h))
		if h == "" {
			return
		}
		for _, e := range out {
			if e == h {
				return
			}
		}
		out = append(out, h)
	}
	add(easyblobkit.RegistryHostFrom(r.Context()))
	add(hostOfBase(a.selfBase()))
	return out
}

// stripOwnHost removes a leading "<own-host>/" segment from a repository key,
// leaving an "<owner>/<name>" (or bare) form. Keys with no deployment-host
// prefix (an explicit upstream host, or a non-registry-Host push) are returned
// unchanged.
func stripOwnHost(key string, own []string) string {
	for _, h := range own {
		if h == "" {
			continue
		}
		if key == h {
			return ""
		}
		if strings.HasPrefix(key, h+"/") {
			return key[len(h)+1:]
		}
	}
	return key
}

// ownerName splits a host-stripped key into (owner, name). Both segments are
// required: a bare key with no "/" is not an owner/name pair.
func ownerName(key string) (owner, name string, ok bool) {
	i := strings.IndexByte(key, '/')
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// hostOfBase extracts "host[:port]" from a scheme://host[:port] base.
func hostOfBase(base string) string {
	b := base
	if i := strings.Index(b, "://"); i >= 0 {
		b = b[i+3:]
	}
	if i := strings.IndexAny(b, "/?#"); i >= 0 {
		b = b[:i]
	}
	if i := strings.LastIndex(b, "@"); i >= 0 {
		b = b[i+1:]
	}
	return b
}

// unscoped returns ctx with an empty RepoScope, so a store call addresses the
// repository key verbatim (no namespace re-qualification).
func unscoped(ctx context.Context) context.Context {
	return easyblobkit.WithRepoScope(ctx, easyblobkit.RepoScope{})
}
