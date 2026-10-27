// Package arch implements the pacman repository protocol with pull-through:
// the repository database (<repo>.db, a gzipped tar of one directory per
// package) and the packages themselves are fetched from the configured
// upstream mirror, cached in the CAS, and served byte-for-byte so pacman's own
// signature checks stay valid.
//
// Wire format (a pacman repository):
//
//	<repo>/os/<arch>/<repo>.db            the database (gzip tar)
//	<repo>/os/<arch>/<repo>.db.tar.gz     the same file under its real name
//	<repo>/os/<arch>/<repo>.files         optional file lists
//	<repo>/os/<arch>/<pkg>.pkg.tar.zst    a package
//
// A database entry is a DIRECTORY per package holding a `desc` file:
//
//	acl-2.4.0-1/
//	acl-2.4.0-1/desc        %FILENAME% / %NAME% / %VERSION% / %CSIZE% / ...
//
// Repository layout served here:
//
//	/artifacts/arch/<repo>/os/<arch>/...   repo is the first path segment
//	                                       ("core", "extra"), so the same mount
//	                                       serves several repositories.
package arch

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// DefaultUpstream is the Arch Linux mirror the adapter proxies when a
// repository has no override in the upstream table.
const DefaultUpstream = "https://geo.mirror.pkgbuild.com"

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// Hosted serves self-published packages and the database generated from
	// them, under the same mount as the proxied repositories.
	Hosted *easyblobkit.HostedHandler
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	s.Hosted = &easyblobkit.HostedHandler{
		Format:    "arch",
		Store:     easyblobkit.HostedStore{Registry: reg},
		Auth:      s.Auth,
		Generator: GenerateDB(easyblobkit.HostedStore{Registry: reg}),
	}
	return s, nil
}

func init() {
	easyblobkit.Register("arch", NewHandler)
	// The first path segment is the repository (core, extra, ...), so per-repo
	// upstream overrides and authorization work like the other system formats.
	easyblobkit.RegisterNamespace("arch", easyblobkit.FirstSegNamespace)
}

// ServeHTTP dispatches one request on the arch mount. A hosted (self-published)
// repository is addressed under the reserved "_hosted/<repo>/" segment; every
// other path is a pull-through cache and is never probed for hosted content.
//
// A pacman client's Server line is "http://mirror/$repo/os/$arch", so a request
// arrives as /artifacts/arch/core/os/x86_64/... and the first segment is the
// repository name.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/arch")
	if hosted {
		s.hosted(w, r, repo, sub)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name, rest, ok := easyblobkit.SplitRepo(sub)
	if !ok {
		// The mount root answers, so a client can tell "wrong path" from
		// "wrong host".
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	s.proxy(w, r, name, rest)
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

// proxy pulls a path through from the repository's upstream mirror.
func (s *State) proxy(w http.ResponseWriter, r *http.Request, name, rest string) {
	base := s.Registry.Upstreams.Repo("arch", name)
	if base == "" {
		base = DefaultUpstream
	}
	path := name + "/" + rest
	ct := mediaTypeOf(rest)
	// Cache by repository + arch, so core and extra never overwrite each other
	// and a stale index in one cannot answer for the other.
	repo := name + "/" + archOf(rest)
	res := s.Registry.FetchCachedPath(r.Context(), "arch", repo, path, "/"+path, base,
		easyblobkit.PathCachePolicy{MediaType: ct, BlobName: path})
	if !res.OK {
		easyblobkit.WriteCacheResultError(w, res)
		return
	}
	if easyblobkit.ServeCachedBlob(w, r, s.Registry.Blobs, r.Context(), res.Artifact, ct, "") {
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "cache error")
}

// archOf extracts the architecture segment from "os/<arch>/..." so cache
// entries group by architecture. Anything else groups under "misc".
func archOf(rest string) string {
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[0] == "os" {
		return parts[1]
	}
	return "misc"
}

// mediaTypeOf maps well-known pacman repository files to media types.
//
// The database is a gzipped tar and is served as such: pacman reads the bytes
// through its own decompressor, so a wrong type would still work but would
// misdescribe the response.
func mediaTypeOf(name string) string {
	base := name
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	switch {
	case strings.HasSuffix(base, ".pkg.tar.zst"):
		return "application/zstd"
	case strings.HasSuffix(base, ".pkg.tar.xz"):
		return "application/x-xz"
	case strings.HasSuffix(base, ".pkg.tar.gz"):
		return "application/gzip"
	case strings.HasSuffix(base, ".db"), strings.HasSuffix(base, ".db.tar.gz"),
		strings.HasSuffix(base, ".files"), strings.HasSuffix(base, ".files.tar.gz"):
		return "application/gzip"
	case strings.HasSuffix(base, ".sig"):
		return "application/pgp-signature"
	default:
		return "application/octet-stream"
	}
}
