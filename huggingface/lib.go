// Package huggingface implements the HuggingFace Hub protocol with
// pull-through: model/dataset metadata, file trees, and (LFS) blobs are
// fetched from huggingface.co, cached in the CAS, and served verbatim.
//
// Wire format (the Hub's REST face used by transformers/diffusers):
//
//	/{repo-type}/{namespace}/{name}/resolve/{revision}/{path}   file content
//	/{repo-type}/{namespace}/{name}/raw/{revision}/{path}       raw text
//	/api/models/{ns}/{name}                                      metadata JSON
//
// repo-type ∈ models | datasets | spaces. The client authenticates with a
// bearer token for gated repos; a per-deployment HF token (from the
// config) is forwarded upstream when set.
//
// Repository layout served here:
//
//	/artifacts/huggingface/{repo-type}/{namespace}/{name}/...
package huggingface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// HFToken is forwarded upstream as a Bearer credential (gated repos).
	HFToken string
	// DisableLFSCache passes the CDN redirect through instead of fetching the
	// bytes server-side. Default (false) caches LFS weights in the CAS.
	DisableLFSCache bool
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["hf_token"].(string); ok {
		s.HFToken = v
	}
	if v, ok := cfg["disable_lfs_cache"].(bool); ok {
		s.DisableLFSCache = v
	}
	return s, nil
}

func init() { easyblobkit.Register("huggingface", NewHandler) }

// upstreamBase is the HF Hub root (overridable for tests via the upstream
// table entry "huggingface").
func (s *State) upstreamBase() string {
	if b := s.Registry.Upstreams.Get("huggingface"); b != "" {
		return b
	}
	return "https://huggingface.co"
}

// repoTypes recognized on the path.
var repoTypes = map[string]bool{"models": true, "datasets": true, "spaces": true}

// mountPath is the prefix this adapter is mounted at. The Hub answers small
// (non-LFS) resolve requests with a HOST-RELATIVE 307 to
// "/api/resolve-cache/..." — passing that Location through verbatim makes the
// client resolve it against the artifact HOST ROOT (a 404) instead of this
// mount. Re-root a root-relative Location under the mount; leave absolute URLs
// (the LFS CDN, https://…) and protocol-relative "//host/…" untouched.
const mountPath = "/artifacts/huggingface"

func rerootLocation(loc string) string {
	if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		return mountPath + loc
	}
	return loc
}

// isRedirect reports whether an upstream status is a redirect we handle.
func isRedirect(code int) bool {
	return code == http.StatusTemporaryRedirect ||
		code == http.StatusFound ||
		code == http.StatusMovedPermanently
}

// resolveLocation turns an upstream Location into an ABSOLUTE URL. The Hub
// answers a SMALL (non-LFS) resolve with a HOST-RELATIVE 307 to
// "/api/resolve-cache/..." — which must be resolved against the UPSTREAM (not
// the artifact mount) so a hub-internal re-resolve can be told apart from an
// external CDN redirect. A protocol-relative "//host/..." takes the upstream's
// scheme; an already-absolute URL is returned as-is.
func (s *State) resolveLocation(loc string) string {
	switch {
	case strings.HasPrefix(loc, "//"):
		return s.upstreamScheme() + ":" + loc
	case strings.HasPrefix(loc, "/"):
		return strings.TrimSuffix(s.upstreamBase(), "/") + loc
	default:
		return loc
	}
}

// upstreamScheme is the upstream base's URL scheme (default https).
func (s *State) upstreamScheme() string {
	if u, err := url.Parse(s.upstreamBase()); err == nil && u.Scheme != "" {
		return u.Scheme
	}
	return "https"
}

// sameHost reports whether two URLs share a host (hub-internal vs CDN).
func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Host == "" {
		return false
	}
	return ua.Host == ub.Host
}

// ServeHTTP dispatches /artifacts/huggingface/{repo-type}/{ns}/{name}/{rest} and
// /artifacts/huggingface/api/{...} metadata calls.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/huggingface/")
	path = strings.TrimPrefix(path, "huggingface/")
	path = strings.Trim(path, "/")
	if path == "" {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	// /api/... is the Hub metadata API (list/search/resolve): a repo key is
	// not derivable from arbitrary API paths, so these are proxied without
	// path-keyed caching (the response is still streamed).
	if strings.HasPrefix(path, "api/") {
		s.proxyUncached(w, r, path)
		return
	}
	parts := strings.Split(path, "/")
	// The repo-type prefix is optional but must be recognized: models have no
	// prefix, datasets/spaces carry one. An unrecognized leading segment is a
	// namespace, NOT a repo type; the remaining repo id is then "{ns}/{name}"
	// (or a bare name). Anything deeper is not a Hub path and 404s.
	repoType := "models"
	if repoTypes[parts[0]] {
		repoType = parts[0]
		parts = parts[1:]
	}
	marker := -1
	for i, s := range parts {
		if s == "resolve" || s == "raw" {
			marker = i
			break
		}
	}
	// The repo id before /resolve|raw/ is a bare name (1 segment) or
	// "{namespace}/{name}" (2 segments). A deeper path is not a Hub repo path
	// (an unrecognized leading segment is a namespace, not a repo type), so
	// "bogus/ns/repo/resolve/..." is a 404, not a model "bogus/ns/repo".
	if marker < 1 || marker > 2 {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	repoID := strings.Join(parts[:marker], "/")
	repoKey := repoType + "/" + repoID
	// The Hub's model URLs have NO repo-type segment ("/{repo}/resolve/..."),
	// while datasets/spaces keep it ("/datasets/{repo}/...").
	upstreamPath := strings.Join(parts, "/")
	if repoType != "models" {
		upstreamPath = repoType + "/" + upstreamPath
	}

	// Local cache first (by full upstream path as version key), streamed.
	// The stored metadata replays the Hub headers (X-Repo-Commit et al).
	if art, err := s.Registry.Meta.Get(r.Context(), "huggingface", repoKey, path); err == nil && len(art.Blobs) > 0 {
		// A 0-byte cached blob is a poisoned entry (an interrupted/empty upstream
		// read was cached). Treat it as a miss so the file self-heals on the next
		// fetch, instead of replaying an empty body forever.
		if sz, _ := s.Registry.Blobs.Stat(r.Context(), art.Blobs[0].Digest); sz != nil && sz.Size > 0 {
			replayStoredHeaders(w, art.Proprietary)
			if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), art.Blobs[0].Digest, art.MediaType) {
				return
			}
		}
	}

	resp, err := s.upstreamGet(r, upstreamPath)
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream fetch: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	// A 307/302/301 to the CDN means an LFS-backed file (weights). Instead of
	// passing the redirect through (which makes the client fetch from the public
	// CDN every time, uncached), fetch the bytes ourselves into the CAS and
	// serve them from here — the same server-side caching the git-lfs adapter
	// does. So a repeated pull hits local storage, and a sandbox with no public
	// egress can still fetch weights (only artifact needs egress).
	if isRedirect(resp.StatusCode) {
		if loc := resp.Header.Get("Location"); loc != "" {
			forwardHubHeaders(w, resp)
			// HEAD must NEVER download the file: the upstream 307 points at a
			// multi-GB CDN object, and fetching it just to answer a HEAD would
			// block for minutes and time the client out. Answer HEAD with the
			// metadata the Hub already gave us (size + commit + etag).
			if r.Method == http.MethodHead {
				if v := resp.Header.Get("X-Linked-Size"); v != "" {
					w.Header().Set("Content-Length", v)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			// The Hub answers a SMALL (non-LFS) resolve with a HOST-RELATIVE
			// 307 to "/api/resolve-cache/..." and a LARGE (LFS) file with an
			// ABSOLUTE 302 to a CDN. Only the CDN (CROSS-HOST) redirect is
			// fetched server-side into the CAS; a hub-internal redirect must be
			// passed through (re-rooted under the mount) so the client
			// re-requests it. Handing the bare host-relative path to the CDN
			// fetcher fails with `unsupported protocol scheme ""` AFTER the 200
			// header is committed — which serves an EMPTY body.
			abs := s.resolveLocation(loc)
			if s.cacheLFS() && !sameHost(abs, s.upstreamBase()) {
				s.serveLFS(w, r, abs, repoKey, path, resp)
				return
			}
			// Pass the redirect through (re-rooted for a hub-relative Location,
			// so the client re-requests it under this mount).
			http.Redirect(w, r, rerootLocation(loc), resp.StatusCode)
			return
		}
	}
	if resp.StatusCode != http.StatusOK {
		easyblobkit.Error(w, resp.StatusCode, "huggingface upstream: "+http.StatusText(resp.StatusCode))
		return
	}
	// The Hub marks its resolve responses with X-Repo-Commit / ETag and the
	// hub client refuses to accept a file without them. Forward the metadata
	// headers a Hub-compatible endpoint must carry.
	forwardHubHeaders(w, resp)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream read: "+err.Error())
		return
	}
	ct := contentType(resp.Header.Get("Content-Type"))
	// Only cache a COMPLETE response: an empty body, or a body shorter than the
	// upstream's advertised Content-Length (a truncated read), must not be
	// indexed — caching it would replay an empty/partial file forever.
	if cacheableResponse(resp, data) {
		if digest := storeCache(s, r.Context(), repoKey, path, data, ct, resp); digest != "" {
			forwardHubHeaders(w, resp)
			if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), digest, ct) {
				return
			}
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", itoa(len(data)))
	_, _ = w.Write(data)
}

// hubHeaderAllow lists the response headers the Hub sets on resolve/raw
// responses that clients require (X-Repo-Commit is mandatory) plus the useful
// caching ones.
var hubHeaderAllow = []string{
	"X-Repo-Commit", "X-Linked-Etag", "X-Linked-Size", "ETag",
	"X-Cache", "Content-Disposition", "X-Error-Message",
	"Access-Control-Expose-Headers",
}

// cacheableResponse reports whether an upstream 200 body is safe to cache: it
// must be non-empty, and (when the upstream declared a Content-Length) the body
// must be exactly that long. An empty or truncated body is NOT cached — that is
// how a 0-byte "poisoned" entry would otherwise be replayed forever.
func cacheableResponse(resp *http.Response, data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if resp.ContentLength > 0 && int64(len(data)) != resp.ContentLength {
		return false
	}
	return true
}

func forwardHubHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, h := range hubHeaderAllow {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	// The Hub advertises the Xet transfer protocol through rel="xet-auth" /
	// rel="xet-reconstruction-info" links. Those point at absolute
	// huggingface.co / cas-server.xethub.hf.co URLs that are unreachable
	// in-cluster, and artifact does not broker the Xet token exchange — so a
	// client that follows them fails with "Xet headers have not been correctly
	// set". Drop the Xet links and forward only the remaining ones; the client
	// then falls back to the classic LFS/CDN path (an absolute 302 that artifact
	// already passes through), which works.
	if links := stripXetLinks(resp.Header.Get("Link")); links != "" {
		w.Header().Set("Link", links)
	}
}

// stripXetLinks removes rel="xet-auth" / rel="xet-reconstruction-info" entries
// from an HTTP Link header, preserving the others verbatim.
func stripXetLinks(link string) string {
	if link == "" || !strings.Contains(link, "xet-") {
		return link
	}
	var kept []string
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="xet-auth"`) || strings.Contains(part, `rel="xet-reconstruction-info"`) {
			continue
		}
		if s := strings.TrimSpace(part); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, ", ")
}

// proxyUncached forwards an /api/... call (metadata/search) and streams the
// response body; 3xx Location is passed through.
func (s *State) proxyUncached(w http.ResponseWriter, r *http.Request, path string) {
	// Preserve the query string: the Hub's /api tree endpoint uses
	// ?recursive=true (and ?expand=, ?cursor=) to shape the listing; dropping
	// it returns only the top-level entries, which makes snapshot_download of a
	// model with subdirectories download just the root files.
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	resp, err := s.upstreamGet(r, path)
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream fetch: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := resp.Header.Get("Location"); loc != "" {
			forwardHubHeaders(w, resp)
			http.Redirect(w, r, rerootLocation(loc), resp.StatusCode)
			return
		}
	}
	ct := contentType(resp.Header.Get("Content-Type"))
	// The final resolve-cache 200 (and other /api responses) must also carry
	// the Hub headers the client requires.
	forwardHubHeaders(w, resp)
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

// upstreamGet issues the Hub request with the optional bearer token.
func (s *State) upstreamGet(r *http.Request, path string) (*http.Response, error) {
	up, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: strings.TrimSuffix(s.upstreamBase(), "/") + "/" + path})
	if s.HFToken != "" {
		up = up.WithHeader("Authorization", "Bearer "+s.HFToken)
	}
	return up.Get(r.Context(), "")
}

// itoa is strconv.Itoa without the extra import at call sites.
func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// storeCache persists a fetched path (best-effort), recording the Hub
// response headers in Proprietary so a cache hit can replay them.
func storeCache(s *State, ctx context.Context, repo, name string, data []byte, mediaType string, resp *http.Response) string {
	stored, err := s.Registry.StoreAndHash(ctx, data)
	if err != nil {
		return ""
	}
	hdrs := map[string]string{}
	for _, h := range hubHeaderAllow {
		if v := resp.Header.Get(h); v != "" {
			hdrs[h] = v
		}
	}
	prop, _ := json.Marshal(map[string]any{"headers": hdrs})
	easyblobkit.LogMetaErr("huggingface cache", s.Registry.Meta.Put(ctx, easyblobkit.Artifact{
		Format: "huggingface", Repository: repo, Version: name,
		MediaType: mediaType, Digest: stored.Digest, Proprietary: prop,
		Blobs:  []easyblobkit.Descriptor{{Digest: stored.Digest, Size: stored.Size, Name: lastSeg(name)}},
		Source: "pull",
	}))
	return stored.Digest
}

// replayStoredHeaders restores the Hub headers recorded for a cached entry.
func replayStoredHeaders(w http.ResponseWriter, prop []byte) {
	if len(prop) == 0 {
		return
	}
	var doc struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(prop, &doc) != nil {
		return
	}
	for k, v := range doc.Headers {
		w.Header().Set(k, v)
	}
}

func contentType(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

func lastSeg(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
