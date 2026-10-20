// Package debian implements the APT repository protocol with pull-through:
// distro indexes (Release, Packages, Sources) and .deb packages are fetched
// from the configured upstream (deb.debian.org / archive.ubuntu.com), cached
// in the CAS, and served byte-for-byte so the distribution's GPG signatures
// stay valid (UpstreamPassthrough strategy — clients keep trusting the
// distro key; local re-signing is a future phase).
//
// Wire format (apt's repository layout):
//
//	dists/<suite>/Release, Release.gpg, InRelease
//	dists/<suite>/<component>/binary-<arch>/Packages[.gz|.xz]
//	pool/<component>/<source>/<deb>
//
// Repository layout served here:
//
//	/artifacts/debian/<distro>/dists/...   distro is the host key ("debian",
//	"ubuntu", ...) so multiple upstreams can coexist.
package debian

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// Hosted serves self-published packages under the same repo key as the
	// proxied archive (unsigned index for [trusted=yes] sources).
	Hosted *easyblobkit.HostedHandler
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	s.Hosted = &easyblobkit.HostedHandler{
		Format:    "debian",
		Store:     easyblobkit.HostedStore{Registry: reg},
		Auth:      s.Auth,
		Generator: GeneratePackages(easyblobkit.HostedStore{Registry: reg}),
	}
	return s, nil
}

func init() {
	easyblobkit.Register("debian", NewHandler)
	// The first path segment is the repository (its upstream may be
	// overridden per repo via -repo-upstreams / the system API).
	easyblobkit.RegisterNamespace("debian", easyblobkit.FirstSegNamespace)
}

// ServeHTTP dispatches one request on the debian mount. A hosted
// (self-published) repository is addressed under the reserved "_hosted/<repo>/"
// segment; every other path is a pull-through cache and is NEVER probed for
// hosted content.
//
// The first path segment of a proxied request is the archive key (an apt source
// of "http://deb.debian.org/debian" arrives as /artifacts/debian/debian/dists/...).
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/debian")
	if hosted {
		s.hosted(w, r, repo, sub)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	archive, rest, ok := easyblobkit.SplitRepo(sub)
	if !ok {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	s.proxy(w, r, archive, rest)
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

// proxy pulls a path through from the archive's upstream mirror.
func (s *State) proxy(w http.ResponseWriter, r *http.Request, archive, rest string) {
	path := archive + "/" + rest
	base := upstreamFor(archive, rest)
	if b := s.Registry.Upstreams.Repo("debian", archive); b != "" && base == "" {
		base = b
	}
	ct := mediaTypeOf(rest)
	// Acquire-By-Hash: serve a by-hash index from the CAS when we already have
	// the bytes (apt fetched the index by name moments before), so an apt update
	// works even when the upstream mirror does not publish by-hash paths. A miss
	// falls through to the upstream proxy below.
	if s.serveByHash(w, r, archive, rest, ct) {
		return
	}
	if base == "" {
		easyblobkit.Error(w, http.StatusNotFound, "unknown archive: "+archive)
		return
	}
	// The client's source keeps the archive prefix in the URL
	// (deb.debian.org/debian/dists/...), so fetch and cache the full path.
	repo := archive + "/" + suiteOf(rest)
	res := s.Registry.FetchCachedPath(r.Context(), "debian", repo, path, "/"+path, base,
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

// upstreamFor picks the mirror host for an archive key + path. The archive
// key is the first path segment of the client's source URL (debian,
// debian-security, ubuntu); the suite selects the security line.
func upstreamFor(archive, path string) string {
	parts := strings.Split(path, "/")
	// Debian security archive (security.debian.org/debian-security/...).
	if archive == "debian-security" {
		return "https://security.debian.org"
	}
	// dists/<suite>/...: only the -security suite lives on the security
	// mirror, while -updates (and -backports/-proposed-updates) stay on the
	// main archive for both distros.
	if len(parts) >= 2 && parts[0] == "dists" {
		suite := parts[1]
		if strings.HasSuffix(suite, "-security") {
			switch archive {
			case "debian":
				return "https://security.debian.org"
			case "ubuntu":
				return "https://security.ubuntu.com"
			}
		}
	}
	// pool/updates/... is the Debian security package pool.
	if len(parts) >= 2 && parts[0] == "pool" && parts[1] == "updates" {
		return "https://security.debian.org"
	}
	switch archive {
	case "debian":
		return "https://deb.debian.org"
	case "ubuntu":
		return "https://archive.ubuntu.com"
	}
	return ""
}

// suiteOf extracts the suite (first path segment under dists/, or "pool" for
// package paths) so cache entries group by distribution line.
func suiteOf(rest string) string {
	parts := strings.Split(rest, "/")
	if len(parts) >= 2 && parts[0] == "dists" {
		return parts[1]
	}
	return "pool"
}

// mediaTypeOf maps well-known apt repository files to media types.
func mediaTypeOf(name string) string {
	switch {
	case name == "InRelease" || strings.HasSuffix(name, "Release.gpg") || strings.HasSuffix(name, "Release"):
		return "application/pgp-signature"
	case strings.HasSuffix(name, ".deb"):
		return "application/vnd.debian.binary-package"
	case strings.HasSuffix(name, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(name, ".xz"):
		return "application/x-xz"
	default:
		return "text/plain"
	}
}
