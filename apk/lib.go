// Package apk implements the Alpine Linux APK repository protocol with
// pull-through: APKINDEX.tar.gz indexes and .apk packages are fetched from
// the configured upstream (dl-cdn.alpinelinux.org), cached in the CAS, and
// served byte-for-byte so the distribution's signatures stay valid.
//
// Wire format (apk's default repository layout):
//
//	/v3.21/main/x86_64/APKINDEX.tar.gz   (gzipped tar of APKINDEX + signature)
//	/v3.21/main/x86_64/<name>-<ver>.apk
//
// Repository layout served here (the "repository root" is the repository
// name — a fixed "main" repo backed by the upstream suite/component):
//
//	/artifacts/apk/<repo>/...  where <repo> encodes suite/component, e.g. "main"
package apk

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// SelfBase is the external base URL for rewritten APKINDEX URLs (the apk
	// client fetches package files relative to the index URL, so no rewrite
	// is needed; SelfBase is reserved for future hosted-repo support).
	SelfBase string
	// Hosted serves /artifacts/apk/hosted/<repo>/... (self-published packages).
	Hosted *easyblobkit.HostedHandler
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["self_base"].(string); ok {
		s.SelfBase = v
	}
	s.Hosted = &easyblobkit.HostedHandler{
		Format:    "apk",
		Store:     easyblobkit.HostedStore{Registry: reg},
		Auth:      s.Auth,
		Generator: GenerateAPKINDEX(easyblobkit.HostedStore{Registry: reg}),
	}
	return s, nil
}

func init() {
	easyblobkit.Register("apk", NewHandler)
	// The first path segment is the repository (its upstream may be
	// overridden per repo via -repo-upstreams / the system API).
	easyblobkit.RegisterNamespace("apk", easyblobkit.FirstSegNamespace)
}

// ServeHTTP dispatches one request on the apk mount. A hosted (self-published)
// repository is addressed under the reserved "_hosted/<repo>/" segment and is
// handled by HostedHandler; every other path is a pull-through cache and is
// NEVER probed for hosted content.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/apk")
	if hosted {
		s.hosted(w, r, repo, sub)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if sub == "" {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	// APK repo keys span multiple path segments (v3.24/main/x86_64), so the
	// whole format-relative path is the proxy path here.
	s.fetch(w, r, sub)
}

// hosted routes an upload/download/delete on a self-published repository.
func (s *State) hosted(w http.ResponseWriter, r *http.Request, repo, rest string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.Hosted.Get(w, r, repo, rest)
	case http.MethodPut, http.MethodPost:
		s.Hosted.Put(w, r, repo, rest)
	case http.MethodDelete:
		s.Hosted.Delete(w, r, repo, rest)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// fetch serves a repository path (index or package) through the local CAS
// with upstream pull-through. Bytes are stored by digest so repeated fetches
// hit the cache and the APKINDEX signature survives verbatim. Freshness
// (single-flight, TTL, conditional revalidation) is shared with the other
// path-tree protocols via FetchCachedPath.
func (s *State) fetch(w http.ResponseWriter, r *http.Request, repoPath string) {
	repo, name := splitRepoPath(repoPath)
	ct := mediaTypeOf(name)
	res := s.Registry.FetchCachedPath(r.Context(), "apk", repo, name, "/"+repoPath, "",
		easyblobkit.PathCachePolicy{MediaType: ct, BlobName: name})
	if !res.OK {
		easyblobkit.WriteCacheResultError(w, res)
		return
	}
	if easyblobkit.ServeCachedBlob(w, r, s.Registry.Blobs, r.Context(), res.Artifact, ct, "") {
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "cache error")
}

// splitRepoPath splits "v3.21/main/x86_64/APKINDEX.tar.gz" into
// repo="v3.21/main/x86_64" and name="APKINDEX.tar.gz" (the last segment).
// The repository (suite/component/arch) is the version key; the file is the
// version value.
func splitRepoPath(repoPath string) (repo, name string) {
	i := strings.LastIndexByte(repoPath, '/')
	if i < 0 {
		return "main", repoPath
	}
	return repoPath[:i], repoPath[i+1:]
}

// mediaTypeOf maps well-known apk repository files to media types.
func mediaTypeOf(name string) string {
	switch {
	case name == "APKINDEX.tar.gz":
		return "application/gzip"
	case strings.HasSuffix(name, ".apk"):
		return "application/vnd.alpine.package.tar"
	default:
		return "application/octet-stream"
	}
}

// RewrittenIndexURLs is a helper for tests: verifies an APKINDEX tar.gz
// contains the expected package entries (no URL rewriting needed — apk
// clients resolve package files relative to the index URL).
func indexContains(indexGz []byte, needle string) bool {
	gz, err := gzip.NewReader(bytes.NewReader(indexGz))
	if err != nil {
		return false
	}
	defer func() { _ = gz.Close() }()
	raw, err := io.ReadAll(gz)
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), needle)
}
