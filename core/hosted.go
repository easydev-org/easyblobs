package easyblobkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
)

// Hosted repository support. A hosted (self-published) repository is addressed
// under a RESERVED path segment so it can never be confused with a pull-through
// cache of the same repository key:
//
//	/artifacts/<format>/_hosted/<repo>/<name>   upload / download / generated index
//	/artifacts/<format>/<repo>/<name>           pull-through cache ONLY (never hosted)
//
// The scope middleware rewrites the hosted form to the adapter's native path
// and records (Namespace=<repo>, Hosted=true) in the RepoScope, so an adapter
// routes on the scope instead of probing: a GET is hosted XOR proxied, never
// "try hosted, fall back to proxy".
//
// The internal metadata repository name is still "hosted/<repo>" (the namespace
// "<repo>" is applied on top by the scoped store), so rows written before this
// addressing change keep resolving unchanged.

// HostedStore is the minimal backend the hosted handler needs (satisfied by
// Registry).
type HostedStore struct {
	Registry *Registry
}

// SplitRepo splits a format-relative path into a repository key (its first
// segment) and the remaining path inside it. rest may be empty (repo root).
func SplitRepo(p string) (repo, rest string, ok bool) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", "", false
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:], true
	}
	return p, "", true
}

// repoKey is the internal metadata repository name for a hosted repo.
func repoKey(repo string) string { return "hosted/" + repo }

// HostedSegment is the reserved path segment that introduces a hosted
// (self-published) repository:
//
//	/artifacts/<format>/_hosted/<repo>/<name>
//
// It is reserved so a pull-through cache and a hosted repo can never share a
// path: the plain form is proxy-only.
const HostedSegment = "_hosted"

// SplitHosted reports whether a format-relative path addresses a hosted
// repository (its first segment is HostedSegment) and, when so, returns the
// repository key and the path inside it:
//
//	"_hosted/<repo>/<rest>" -> (repo, rest, true)
//	"_hosted/<repo>"        -> (repo, "", true)
//	anything else           -> ("", p, false)
func SplitHosted(p string) (repo, rest string, ok bool) {
	first, after, split := SplitRepo(p)
	if !split || first != HostedSegment {
		return "", p, false
	}
	repo, rest, ok = SplitRepo(after)
	if !ok || repo == "" {
		return "", "", false
	}
	return repo, rest, true
}

// HostedRequest resolves a request's hosted addressing into (repo, nativePath,
// hosted). It prefers the RepoScope (the /artifacts middleware rewrites the
// "_hosted/<repo>/" form to the native path and sets Namespace/Hosted), and
// falls back to parsing the request path directly so an adapter driven without
// the middleware — a unit test, an embedder — sees the same routing. mount is
// the format's mount prefix ("/artifacts/apk", or "/v2" for OCI).
func HostedRequest(r *http.Request, mount string) (repo, nativePath string, hosted bool) {
	if sc := RepoScopeFrom(r.Context()); sc.Hosted {
		// The middleware already rewrote "_hosted/<repo>/<inner>" to <inner>.
		return sc.Namespace, formatPathUnder(r.URL.Path, mount), true
	}
	return SplitHosted(hostedPathUnder(r.URL.Path, mount))
}

// formatPathUnder strips the mount prefix from p and returns the
// format-relative path with no leading slash. The first segment is preserved
// verbatim: a plain path whose first segment happens to equal the mount name is
// part of the upstream path (debian's archive key "debian" arrives as
// /artifacts/debian/debian/dists/...) and must NOT be truncated.
func formatPathUnder(p, mount string) string {
	p = strings.TrimPrefix(p, mount)
	p = strings.TrimPrefix(p, "/")
	return strings.Trim(p, "/")
}

// hostedPathUnder is formatPathUnder plus the historical tolerance for a
// repeated format segment before the hosted marker
// ("/artifacts/apk/apk/_hosted/<repo>/..."). It is used ONLY to resolve hosted
// addressing, and it strips the repeated segment only when "_hosted" follows —
// so a plain path (no "_hosted") is never truncated, even when its first
// segment equals the mount name.
func hostedPathUnder(p, mount string) string {
	p = formatPathUnder(p, mount)
	seg := mountName(mount)
	if seg == "" || !strings.HasPrefix(p, seg+"/") {
		return p
	}
	if rest := strings.TrimPrefix(p, seg+"/"); rest == HostedSegment || strings.HasPrefix(rest, HostedSegment+"/") {
		return rest
	}
	return p
}

// mountName returns the last path element of a mount ("/artifacts/apk" -> "apk").
func mountName(mount string) string {
	if i := strings.LastIndexByte(mount, '/'); i >= 0 {
		return mount[i+1:]
	}
	return mount
}

// HostedUpload streams uploaded bytes under (format, repo, name) into the CAS
// and indexes them, without buffering the whole body in memory. It returns the
// digest and size.
func (h HostedStore) HostedUpload(ctx context.Context, format, repo, name string, r io.Reader) (string, int64, error) {
	stored, err := h.Registry.StoreStream(ctx, r)
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	if err := h.Registry.Meta.Put(ctx, Artifact{
		Format: format, Repository: repoKey(repo), Version: name,
		MediaType: "application/octet-stream", Digest: stored.Digest,
		Blobs:  []Descriptor{{Digest: stored.Digest, Size: stored.Size, Name: baseName(name)}},
		Source: "push", StoredAt: now, PublishedAt: now,
	}); err != nil {
		return "", 0, err
	}
	return stored.Digest, stored.Size, nil
}

// HostedFile returns the stored digest for one hosted file ("" when absent).
func (h HostedStore) HostedFile(ctx context.Context, format, repo, name string) (string, bool) {
	art, err := h.Registry.Meta.Get(ctx, format, repoKey(repo), name)
	if err != nil || len(art.Blobs) == 0 {
		return "", false
	}
	return art.Blobs[0].Digest, true
}

// HostedFiles returns every hosted file in the repo with its digest and size in
// ONE index pass, rather than a Get + Stat per name. Index generation uses it
// so a repo with N packages costs one query.
func (h HostedStore) HostedFiles(ctx context.Context, format, repo string) ([]HostedFile, error) {
	arts, err := h.Registry.Meta.ListArtifacts(ctx, format, repoKey(repo))
	if err != nil {
		return nil, err
	}
	out := make([]HostedFile, 0, len(arts))
	for i := range arts {
		if len(arts[i].Blobs) == 0 {
			continue
		}
		b := arts[i].Blobs[0]
		out = append(out, HostedFile{Name: arts[i].Version, Digest: b.Digest, Size: b.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// HostedDelete removes one hosted file and its CAS blob when unreferenced.
func (h HostedStore) HostedDelete(ctx context.Context, format, repo, name string) error {
	digest, ok := h.HostedFile(ctx, format, repo, name)
	if !ok {
		return nil
	}
	if err := h.Registry.Meta.Delete(ctx, format, repoKey(repo), name); err != nil {
		return err
	}
	// Best-effort blob GC: only delete when no other artifact references it.
	if !h.digestReferenced(ctx, digest) {
		LogMetaErr("hosted gc", h.Registry.Blobs.Delete(ctx, digest))
	}
	return nil
}

// digestReferenced reports whether any artifact still points at the digest.
// It asks the index for the full reference set in one pass, rather than
// loading every artifact one by one.
func (h HostedStore) digestReferenced(ctx context.Context, digest string) bool {
	refs, err := h.Registry.Meta.ReferencedDigests(ctx)
	if err != nil {
		return true // be conservative
	}
	return refs[digest]
}

// GeneratedFile is one index document produced by a Generator.
type GeneratedFile struct {
	Body        []byte
	ContentType string
}

// Generator renders the repo's index document(s) from its file list: a map
// from hosted path name to file. Multi-file indexes (rpm's repomd.xml plus
// the primary metadata it references) return several entries. Regeneration
// is deterministic from `files`, so no caching layer is needed. ctx carries the
// request's cancellation/deadline into any blob reads the generator performs.
type Generator func(ctx context.Context, files []HostedFile) (map[string]GeneratedFile, error)

// HostedFile is one stored file, as seen by a Generator.
type HostedFile struct {
	Name   string
	Digest string
	Size   int64
}

// HostedHandler serves an upload/generated-index repo for one format.
type HostedHandler struct {
	Format    string
	Store     HostedStore
	Auth      Auth
	Generator Generator
}

// Get serves a hosted file or generated index from repo. It ALWAYS handles the
// request: a miss is a 404, never a signal to fall back to proxying. A hosted
// path is hosted-only (see HostedSegment), so there is nothing to fall back to.
//
//	repo  the repository key (the segment after "_hosted")
//	name  the path inside the repo
func (h *HostedHandler) Get(w http.ResponseWriter, r *http.Request, repo, name string) {
	if h == nil {
		Error(w, http.StatusNotFound, "not found")
		return
	}
	name = path.Clean(name)
	if name == "." || name == "" {
		Error(w, http.StatusNotFound, "not found")
		return
	}
	// A generated index (e.g. repodata/repomd.xml, Packages) takes priority
	// because it is derived, not stored. The whole repo is loaded in one pass
	// so regenerating an N-file index costs one query, not N.
	if h.Generator != nil {
		if files, err := h.Store.HostedFiles(r.Context(), h.Format, repo); err == nil && len(files) > 0 {
			if gf, ok := h.generate(r.Context(), files, name); ok {
				w.Header().Set("Content-Type", gf.ContentType)
				w.Header().Set("Content-Length", fmt.Sprint(len(gf.Body)))
				w.WriteHeader(http.StatusOK)
				if r.Method != http.MethodHead {
					_, _ = w.Write(gf.Body)
				}
				return
			}
		}
	}
	if digest, ok := h.Store.HostedFile(r.Context(), h.Format, repo, name); ok {
		if ServeBlobAt(w, r, h.Store.Registry.Blobs, r.Context(), digest, "application/octet-stream") {
			return
		}
	}
	Error(w, http.StatusNotFound, "not found")
}

// Put stores an uploaded file into repo.
func (h *HostedHandler) Put(w http.ResponseWriter, r *http.Request, repo, name string) {
	// The repo is the namespace for the hosted formats (apk/debian/rpm/conda):
	// scope-aware authorization keeps a repo's contents under one owner.
	if !AuthorizeWriteFor(w, r, h.Auth, h.Store.Registry, h.Format, name) {
		return
	}
	if _, _, err := h.Store.HostedUpload(r.Context(), h.Format, repo, name, r.Body); err != nil {
		Error(w, http.StatusBadRequest, "upload: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// Delete removes a hosted file from repo.
func (h *HostedHandler) Delete(w http.ResponseWriter, r *http.Request, repo, name string) {
	if !AuthorizeWriteFor(w, r, h.Auth, h.Store.Registry, h.Format, name) {
		return
	}
	if err := h.Store.HostedDelete(r.Context(), h.Format, repo, name); err != nil {
		Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// generate renders the repo index from an already-loaded file list and returns
// the requested member. The caller supplies the list (one index pass), so
// generation adds no per-file queries.
func (h *HostedHandler) generate(ctx context.Context, files []HostedFile, name string) (GeneratedFile, bool) {
	out, err := h.Generator(ctx, files)
	if err != nil {
		return GeneratedFile{}, false
	}
	gf, ok := out[name]
	return gf, ok
}

// baseName is path.Base without the os import.
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
