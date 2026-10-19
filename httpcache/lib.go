// Package httpcache mirrors the package repositories that are plain HTTP
// directory trees with no protocol of their own: Hackage (Haskell), CRAN (R),
// CPAN (Perl) and LuaRocks. A client fetches an index file and then the
// tarball the index names:
//
//	hackage.haskell.org/01-index.tar.gz
//	hackage.haskell.org/package/<pkg>-<ver>/<pkg>-<ver>.tar.gz
//	cran.r-project.org/src/contrib/PACKAGES.gz
//	cran.r-project.org/src/contrib/<pkg>_<ver>.tar.gz
//	cpan.metacpan.org/modules/02packages.details.txt.gz
//	cpan.metacpan.org/authors/id/<A>/<AB>/<AUTHOR>/<Dist>.tar.gz
//	luarocks.org/manifest-5.4
//	luarocks.org/<rock>.src.rock
//	pkg.julialang.org/registries
//	pkg.julialang.org/meta
//	pkg.julialang.org/<uuid>/<treehash>
//
// One implementation covers all of them: a path-keyed pull-through cache.
// Each tree is its own format (so the upstream table, repository namespace and
// per-repo overrides all work per tree).
//
// Repository layout served here:
//
//	/artifacts/<tree>/<path>    e.g. /artifacts/cran/src/contrib/PACKAGES.gz
//
// Host-driven mode works too: the egress proxy rewrites
// https://cran.r-project.org/... onto /artifacts/cran/... with the original Host
// preserved, so the mirror is transparent to an unmodified client.
package httpcache

import (
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// trees lists the formats this package serves. The value is only a hint for
// documentation: the upstream comes from the registry's table (or the client's
// Host), never from here.
//
// Most are plain package trees; a few carry a JSON API on the same paths:
//   - jsr     JSR's native registry API: /@scope/pkg/meta.json,
//     /@scope/pkg/<ver>_meta.json and /@scope/pkg/<ver>/<path> modules.
//   - bazel   Bazel Central Registry: /modules/<name>/<ver>/*.json + source.json.
//   - jenkins Jenkins update center: /download/plugins/... and update-center.json.
//   - opam    opam repository index + cache archives.
var trees = []string{
	"hackage", "cran", "cpan", "luarocks", "juliapkg",
	"jsr", "opam", "stackage", "pecl", "bazel", "jenkins",
}

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// Tree is the format this handler serves (hackage, cran, cpan, luarocks).
	Tree string
	// Hosted serves /artifacts/<tree>/_hosted/<repo>/... (self-published files
	// plus a generated index). A hosted repo is addressed under the reserved
	// "_hosted" segment, so it can never be confused with the pull-through
	// cache of the same path.
	Hosted *easyblobkit.HostedHandler
}

func newHandler(tree string) func(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	return func(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
		s := &State{Registry: reg, Tree: tree}
		if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
			s.Auth = a
		}
		s.Hosted = &easyblobkit.HostedHandler{
			Format:    tree,
			Store:     easyblobkit.HostedStore{Registry: reg},
			Auth:      s.Auth,
			Generator: GenerateTreeIndex(easyblobkit.HostedStore{Registry: reg}, tree),
		}
		return s, nil
	}
}

func init() {
	for _, tree := range trees {
		easyblobkit.Register(tree, newHandler(tree))
		// A hosted repo's namespace is its first path segment, so per-repo
		// authorization and upstream overrides work like the system formats.
		easyblobkit.RegisterNamespace(tree, easyblobkit.FirstSegNamespace)
	}
}

// ServeHTTP serves one tree path, cache-first. A hosted (self-published) repo
// is addressed under the reserved "_hosted/<repo>/" segment and handled by the
// HostedHandler (upload / download / generated index); every other path is a
// read-only pull-through cache.
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if repo, sub, hosted := easyblobkit.HostedRequest(r, "/artifacts/"+s.Tree); hosted {
		s.serveHosted(w, r, repo, sub)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/"+s.Tree)
	path = strings.TrimPrefix(path, "/"+s.Tree)
	path = strings.Trim(path, "/")
	if path == "" {
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"tree": s.Tree})
		return
	}
	// Cache-first with shared freshness handling (single-flight, TTL +
	// conditional revalidation). The body streams into the CAS, so a client's
	// Range request is satisfied locally after the first fetch; these trees
	// carry 100MB+ index files.
	ct := mediaTypeOf(path)
	res := s.Registry.FetchCachedPath(r.Context(), s.Tree, "tree", path, "/"+path, "",
		easyblobkit.PathCachePolicy{MediaType: ct, BlobName: pathBase(path)})
	if !res.OK {
		easyblobkit.WriteCacheResultError(w, res)
		return
	}
	if easyblobkit.ServeCachedBlob(w, r, s.Registry.Blobs, r.Context(), res.Artifact, ct, pathBase(path)) {
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "cache error")
}

// serveHosted routes an upload/download/delete on a self-published repository.
func (s *State) serveHosted(w http.ResponseWriter, r *http.Request, repo, rest string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.Hosted.Get(w, r, repo, rest)
	case http.MethodPut:
		s.Hosted.Put(w, r, repo, rest)
	case http.MethodDelete:
		s.Hosted.Delete(w, r, repo, rest)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// mediaTypeOf keeps the content type the client expects for cached bytes.
func mediaTypeOf(p string) string {
	switch {
	case strings.HasSuffix(p, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(p, ".xz"):
		return "application/x-xz"
	case strings.HasSuffix(p, ".json"):
		return "application/json"
	case strings.HasSuffix(p, ".ts"), strings.HasSuffix(p, ".mts"), strings.HasSuffix(p, ".cts"):
		return "application/typescript"
	case strings.HasSuffix(p, ".js"), strings.HasSuffix(p, ".mjs"), strings.HasSuffix(p, ".cjs"):
		return "text/javascript"
	case strings.HasSuffix(p, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(p, ".md"):
		return "text/markdown"
	case strings.HasSuffix(p, ".txt"):
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
