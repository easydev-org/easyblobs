// Package git implements Git's smart-HTTP protocol as a caching pull-through
// mirror with a **local bare mirror** per repository. The first clone populates
// the mirror from the upstream; every later clone/fetch is served from local
// objects, so a repeated clone is fast and works even when the upstream is
// unreachable.
//
// Repository layout served here:
//
//	/artifacts/git/<host>/<path>            e.g. /artifacts/git/github.com/julialang/general
//
// The host is the first path segment (github.com, gitlab.com, ...), so one
// instance mirrors any number of git servers and the host is the repository
// namespace (isolation + per-host upstream overrides).
//
// Implementation note: the mirror uses the host's git CLI (`git clone --mirror`
// on first use, `git fetch --prune` afterwards) and serves the smart-HTTP
// protocol with `git upload-pack --stateless-rpc` / `git receive-pack
// --stateless-rpc` (the same pair CGI servers use, i.e. what `git http-backend`
// drives). That keeps pack negotiation correct without reimplementing the
// protocol, and pushes land in the local mirror.
package git

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// Dir roots the bare mirrors (usually <data>/git). Empty uses
	// <TempDir>/artifact-git.
	Dir string
	// SelfBase is the external base URL the client used, for emitted URLs.
	SelfBase string
	// Timeout bounds one upstream git command (clone/fetch). Empty = 10m.
	Timeout time.Duration
	// Refresh is how long a mirror may serve stale content before a background
	// fetch is kicked off. Empty = 1m. A request never waits for the refresh;
	// only the very first clone of a repository is synchronous.
	Refresh time.Duration
	// DisableLocal writes through to the upstream without keeping a mirror
	// (a pure proxy). Off by default.
	DisableLocal bool

	mu         sync.Mutex
	refreshing map[string]bool
	// locks is a fixed stripe table: hashing a repository key onto one of a
	// small number of mutexes serialises clone/fetch per repository WITHOUT an
	// unbounded map that would grow with the number of repositories ever seen.
	locks [lockStripes]sync.Mutex
}

// lockStripes is the size of the repository-lock stripe table. It only needs
// to exceed the number of repositories fetched CONCURRENTLY (a hash collision
// merely serialises two unrelated repos briefly).
const lockStripes = 64

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["dir"].(string); ok && v != "" {
		s.Dir = v
	}
	if v, ok := cfg["self_base"].(string); ok {
		s.SelfBase = strings.TrimSuffix(v, "/")
	}
	if v, ok := cfg["timeout"].(time.Duration); ok {
		s.Timeout = v
	}
	if v, ok := cfg["disable_local"].(bool); ok {
		s.DisableLocal = v
	}
	return s, nil
}

func init() { easyblobkit.Register("git", NewHandler) }

// maxBody bounds a pack upload (a push of a large history can be big).
const maxBody = 2 << 30

func (s *State) dir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return filepath.Join(os.TempDir(), "artifact-git")
}

func (s *State) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 10 * time.Minute
}

// ServeHTTP dispatches the smart-HTTP surface of one upstream repository. The
// upstream host is the first path segment:
//
//	git clone http://gateway/artifacts/git/github.com/me/repo
//
// (the client is pointed straight at this mirror; there is no intercepting
// sidecar to carry the origin in X-Forwarded-Host).
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/artifacts/git/"), "/")
	path = strings.TrimPrefix(path, "git/")

	host, repoPath, ok := easyblobkit.SplitRepo(path)
	if !ok || host == "" || repoPath == "" {
		easyblobkit.Error(w, http.StatusNotFound, "host and repository required")
		return
	}
	s.dispatch(w, r, easyblobkit.CanonicalHost(host), repoPath)
}

// dispatch routes one repository's endpoints.
func (s *State) dispatch(w http.ResponseWriter, r *http.Request, host, repoPath string) {

	// Service endpoints (git-upload-pack / git-receive-pack and their
	// discovery) operate on the mirror; everything else (dumb objects) is
	// proxied so odd clients still work.
	switch {
	case strings.HasSuffix(repoPath, "/info/refs"):
		s.infoRefs(w, r, host, strings.TrimSuffix(repoPath, "/info/refs"))
	case strings.HasSuffix(repoPath, "/git-upload-pack"):
		s.service(w, r, host, strings.TrimSuffix(repoPath, "/git-upload-pack"), "upload-pack", false)
	case strings.HasSuffix(repoPath, "/git-receive-pack"):
		s.service(w, r, host, strings.TrimSuffix(repoPath, "/git-receive-pack"), "receive-pack", true)
	// Git LFS: the batch negotiation is proxied (its action hrefs are rewritten
	// to the mirror), and object transfers are served from the CAS, fetching
	// through from the upstream's LFS object URL on a miss.
	case strings.HasSuffix(repoPath, "/info/lfs/objects/batch") && r.Method == http.MethodPost:
		s.lfsBatch(w, r, host, strings.TrimSuffix(repoPath, "/info/lfs/objects/batch"))
	case strings.HasSuffix(repoPath, "/info/lfs/objects/batch") && r.Method == http.MethodGet:
		s.proxy(w, r, host, repoPath)
	case strings.Contains(repoPath, "/info/lfs/objects/"):
		repo := repoPath[:strings.Index(repoPath, "/info/lfs/objects/")]
		rest := repoPath[strings.Index(repoPath, "/info/lfs/objects/")+len("/info/lfs/objects/"):]
		verb, oid, _ := strings.Cut(rest, "/")
		s.lfsObject(w, r, host, repo, verb, oid)
	default:
		s.proxy(w, r, host, repoPath)
	}
}
