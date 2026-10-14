// Package rpm implements the YUM/DNF repository protocol with pull-through:
// repodata (repomd.xml, primary/other/filelists XML) and .rpm packages are
// fetched from the configured upstream, cached in the CAS, and served
// byte-for-byte so repository signatures stay valid.
//
// Wire format (a yum repo layout):
//
//	repodata/repomd.xml           (index of the metadata files, signed)
//	repodata/*-primary.xml.gz     (package index)
//	.../packages/.../x.rpm
//
// Repository layout served here:
//
//	/artifacts/rpm/<repo>/<path>   where <repo> is a user-chosen repository key
//	(e.g. "fedorarelease41"); the upstream base for each repo key is resolved
//	from the upstream table by repo name, and the key is ALSO the first segment
//	of the upstream path (the client's base URL contains it).
package rpm

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// RepoUpstreams maps repository keys to upstream bases. When nil, the
	// single "rpm" upstream from the registry table is used for every repo.
	RepoUpstreams map[string]string
	// Hosted serves /artifacts/rpm/hosted/<repo>/... (self-published packages).
	Hosted *easyblobkit.HostedHandler
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if m, ok := cfg["repo_upstreams"].(map[string]string); ok {
		s.RepoUpstreams = m
	}
	s.Hosted = &easyblobkit.HostedHandler{
		Format:    "rpm",
		Store:     easyblobkit.HostedStore{Registry: reg},
		Auth:      s.Auth,
		Generator: GenerateRepodata(easyblobkit.HostedStore{Registry: reg}),
	}
	return s, nil
}

func init() {
	easyblobkit.Register("rpm", NewHandler)
	// The first path segment is the repository (its upstream may be
	// overridden per repo via -repo-upstreams / the system API).
	easyblobkit.RegisterNamespace("rpm", easyblobkit.FirstSegNamespace)
}

// ServeHTTP dispatches one request on the rpm mount. A hosted (self-published)
// repository is addressed under the reserved "_hosted/<repo>/" segment; every
// other path is a pull-through cache and is NEVER probed for hosted content.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/rpm")
	if hosted {
		s.hosted(w, r, repo, sub)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	key, rest, ok := easyblobkit.SplitRepo(sub)
	if !ok {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	s.proxy(w, r, key, rest)
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

// proxy pulls a repository path through from its upstream mirror.
//
// The repo key is the first path segment (the Fedora client's base URL
// includes it: dl.fedoraproject.org/pub/fedora/... — the repo key is "pub"),
// so the key is BOTH the upstream selector and part of the path that is
// fetched and cached. The fetch path therefore carries the full
// format-relative path, exactly like debian's archive segment: dropping it
// would fetch dl.fedoraproject.org/... instead of .../pub/... .
func (s *State) proxy(w http.ResponseWriter, r *http.Request, repo, rest string) {
	base := s.upstreamFor(repo)
	if base == "" {
		easyblobkit.Error(w, http.StatusNotFound, "unknown rpm repo: "+repo)
		return
	}
	full := repo
	if rest != "" {
		full = repo + "/" + rest
	}

	ct := mediaTypeOf(rest)
	res := s.Registry.FetchCachedPath(r.Context(), "rpm", repo, full, "/"+full, base,
		easyblobkit.PathCachePolicy{MediaType: ct, BlobName: rest})
	if !res.OK {
		easyblobkit.WriteCacheResultError(w, res)
		return
	}
	if easyblobkit.ServeCachedBlob(w, r, s.Registry.Blobs, r.Context(), res.Artifact, ct, "") {
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "cache error")
}

// upstreamFor resolves the upstream base for a repository key: an explicit
// override (legacy config map, then -repo-upstreams) wins, else the format
// default.
func (s *State) upstreamFor(repo string) string {
	if s.RepoUpstreams != nil {
		if b, ok := s.RepoUpstreams[repo]; ok {
			return b
		}
	}
	return s.Registry.Upstreams.Repo("rpm", repo)
}

// mediaTypeOf maps well-known yum repository files to media types.
func mediaTypeOf(name string) string {
	switch {
	case strings.HasSuffix(name, "repomd.xml"):
		return "application/xml"
	case strings.HasSuffix(name, ".rpm"):
		return "application/x-rpm"
	case strings.HasSuffix(name, ".xml.gz") || strings.HasSuffix(name, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(name, ".xml.zck"):
		return "application/zstd"
	default:
		return "application/octet-stream"
	}
}
