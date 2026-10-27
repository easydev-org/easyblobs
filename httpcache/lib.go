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
//   - bazel   Bazel Central Registry: /modules/<name>/<ver>/*.json + source.json.
//   - jenkins Jenkins update center: /download/plugins/... and update-center.json.
//   - opam    opam repository index + cache archives.
//   - aur     the Arch User Repository: /rpc/v5/{info,search}, /cgit/aur.git/
//             {plain/PKGBUILD,snapshot/<pkg>.tar.gz} and /packages.gz.
//
// AUR is READ-ONLY here on purpose. Its publish direction is an SSH git push to
// aur@aur.archlinux.org, which has no self-hosted equivalent and no HTTP
// endpoint - so there is nothing for a hosted repository to stand in for. What
// yay and paru actually need is the three read endpoints, and those are plain
// HTTP GETs:
//
//   AURRPCURL + /rpc/v5/info?arg[]=<pkg>          package metadata
//   AURURL    + /cgit/aur.git/plain/PKGBUILD?h=<pkg>  the build script
//   AURURL    + /cgit/aur.git/snapshot/<pkg>.tar.gz   the source snapshot
//
// Unlike jsr.io, AUR's clients DO let you point them elsewhere: yay reads
// aururl and aurrpcurl from its config (`yay --aururl`, `--aurrpcurl`), so a
// mirror is usable rather than theoretical.
//
// JSR's native registry is deliberately NOT here. deno hard-codes jsr.io and
// offers no way to point it elsewhere (there is no DENO_*_REGISTRY variable;
// DENO_LOG=debug shows it fetching https://jsr.io/... regardless), so a hosted
// JSR repo could be documented perfectly and still be uninstallable by the only
// client that speaks the protocol. JSR packages are still reachable - through
// the npm-compatible endpoint at npm.jsr.io, which is the npm protocol and is
// tested as such.
var trees = []string{
	"hackage", "cran", "cpan", "luarocks", "juliapkg",
	"opam", "stackage", "pecl", "bazel", "jenkins", "aur",
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
	// The stored version is the PATH ITSELF (plus any query).
	//
	// FetchCachedPath keys the cache by (format, repo, version) and ignores the
	// path when reading, so a constant version makes every file in a tree share
	// one cache entry: the first object fetched answers for all the rest. That
	// is exactly what happened when this said "tree":
	//
	//   trying URL '<mount>/src/contrib/jsonlite_2.0.0.tar.gz'
	//   Content type 'application/gzip' length 7414716 bytes
	//   Error in getOctD(x, offset, len) : invalid octal digit
	//
	// 7414716 bytes is the size of src/contrib/PACKAGES, the file fetched a
	// moment earlier - the package request was served the index's bytes.
	ver := path
	if q := r.URL.RawQuery; q != "" {
		ver = path + "?" + q
	}
	res := s.Registry.FetchCachedPath(r.Context(), s.Tree, "tree", ver, "/"+path, "",
		easyblobkit.PathCachePolicy{
			MediaType: ct,
			BlobName:  pathBase(path),
			// The query is part of the request, not decoration. AUR's RPC:
			//   /rpc/v5/info?arg[]=<pkg>
			// answers {"error":"No request type/data specified.","results":[]}
			// when the selector is dropped, and that reads like an empty result
			// rather than a lost parameter. The same applies to cgit's ?h=<ref>.
			Query: r.URL.RawQuery,
		})
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
