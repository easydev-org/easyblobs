// Package conda implements the Conda channel protocol with pull-through:
// repodata.json per subdir, channeldata.json, and package archives
// (.conda/.tar.bz2) are fetched from the configured upstream
// (repo.anaconda.com / conda.anaconda.org), cached in the CAS, and served
// byte-for-byte (conda verifies package hashes recorded in repodata).
//
// Wire format (a conda channel layout):
//
//	/channeldata.json
//	/<subdir>/repodata.json        (e.g. linux-64, noarch)
//	/<subdir>/<pkg>.conda|.tar.bz2
//
// Repository layout served here:
//
//	/artifacts/conda/{channel}/{path}   channel = repo key ("main", "conda-forge", ...)
package conda

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// ChannelUpstreams maps channel keys to upstream bases; nil uses the
	// single "conda" upstream default.
	ChannelUpstreams map[string]string
	// Hosted serves /artifacts/conda/hosted/<channel>/... (self-published).
	Hosted *easyblobkit.HostedHandler
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if m, ok := cfg["channel_upstreams"].(map[string]string); ok {
		s.ChannelUpstreams = m
	}
	s.Hosted = &easyblobkit.HostedHandler{
		Format:    "conda",
		Store:     easyblobkit.HostedStore{Registry: reg},
		Auth:      s.Auth,
		Generator: GenerateRepodata(easyblobkit.HostedStore{Registry: reg}),
	}
	return s, nil
}

func init() {
	easyblobkit.Register("conda", NewHandler)
	// The first path segment is the repository (its upstream may be
	// overridden per repo via -repo-upstreams / the system API).
	easyblobkit.RegisterNamespace("conda", easyblobkit.FirstSegNamespace)
}

// ServeHTTP dispatches one request on the conda mount. A hosted (self-published)
// channel is addressed under the reserved "_hosted/<repo>/" segment; every other
// path is a pull-through cache and is NEVER probed for hosted content.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/conda")
	if hosted {
		s.hosted(w, r, repo, sub)
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	channel, rest, ok := easyblobkit.SplitRepo(sub)
	if !ok {
		easyblobkit.Error(w, http.StatusNotFound, "path required")
		return
	}
	s.proxy(w, r, sub, channel, rest)
}

// hosted routes an upload/download/delete on a self-published channel.
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

// proxy pulls a channel path through from its upstream. The client's original
// path is preserved by the egress rewrite, so it already mirrors the upstream
// layout exactly:
//
//	repo.anaconda.com/pkgs/<channel>/<subdir>/repodata.json
//	conda.anaconda.org/<channel>/<subdir>/repodata.json
//
// path is the full format-relative path and channel its first segment.
func (s *State) proxy(w http.ResponseWriter, r *http.Request, path, channel, rest string) {
	base := s.upstreamFor(channel)
	if base == "" {
		easyblobkit.Error(w, http.StatusNotFound, "unknown conda channel: "+channel)
		return
	}

	repo := channel + "/" + subdirOf(rest)
	ct := mediaTypeOf(path)
	res := s.Registry.FetchCachedPath(r.Context(), "conda", repo, path, "/"+path, base,
		easyblobkit.PathCachePolicy{MediaType: ct})
	if !res.OK {
		easyblobkit.WriteCacheResultError(w, res)
		return
	}
	if easyblobkit.ServeCachedBlob(w, r, s.Registry.Blobs, r.Context(), res.Artifact, ct, "") {
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "cache error")
}

// upstreamFor resolves the upstream base for a channel key: a per-channel
// explicit override (the legacy config map, then -repo-upstreams) wins, else
// the format default.
func (s *State) upstreamFor(channel string) string {
	if s.ChannelUpstreams != nil {
		if b, ok := s.ChannelUpstreams[channel]; ok {
			return b
		}
	}
	return s.Registry.Upstreams.Repo("conda", channel)
}

// subdirOf extracts the conda subdir for cache grouping ("linux-64",
// "noarch", or the top file for channeldata).
func subdirOf(rest string) string {
	if rest == "channeldata.json" {
		return ""
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func mediaTypeOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".json") || name == "channeldata.json":
		return "application/json"
	case strings.HasSuffix(name, ".conda"):
		return "application/zip"
	case strings.HasSuffix(name, ".tar.bz2"):
		return "application/x-tar"
	case strings.HasSuffix(name, ".zst"):
		return "application/zstd"
	default:
		return "application/octet-stream"
	}
}
