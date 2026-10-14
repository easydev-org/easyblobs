package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

// mirrorPath is the on-disk bare mirror for host/repo.
func (s *State) mirrorPath(host, repo string) string {
	sum := sha256.Sum256([]byte(host + "/" + repo))
	return filepath.Join(s.dir(), host, hex.EncodeToString(sum[:8])+".git")
}

// repoLock returns the striped mutex that serialises clone/fetch for a
// repository key. Two keys colliding on a stripe only briefly serialise.
func (s *State) repoLock(key string) *sync.Mutex {
	sum := sha256.Sum256([]byte(key))
	return &s.locks[sum[0]%lockStripes]
}

// ensureMirror makes the local mirror exist and reasonably fresh. It is a
// no-op when local storage is disabled.
//
// Freshness policy: a request NEVER blocks on the upstream when a mirror
// already exists — it is served from local objects immediately and a
// background refresh is kicked off (at most one at a time per repository, and
// only when the mirror is older than Refresh). The first request for a
// repository must clone synchronously because there is nothing to serve yet.
func (s *State) ensureMirror(ctx context.Context, host, repo string) (string, error) {
	if s.DisableLocal {
		return "", nil
	}
	dir := s.mirrorPath(host, repo)
	key := host + "/" + repo
	mu := s.repoLock(key)
	mu.Lock()
	defer mu.Unlock()

	if isGitDir(dir) {
		s.maybeRefresh(dir, host, repo)
		return dir, nil
	}

	tctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	upstream, err := s.upstreamURL(ctx, host, repo)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(tctx, "git", "clone", "--mirror", upstream, dir)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", &gitError{Op: "clone", Err: err, Out: string(out), NotFound: upstreamNotFound(string(out))}
	}
	_ = os.WriteFile(dir+"/.artifact-fetched", []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
	return dir, nil
}

// maybeRefresh refreshes a mirrored repository in the background when it is
// older than Refresh. The caller holds the repository lock; the refresh runs
// on its own goroutine with its own lock so it does not serialize requests.
func (s *State) maybeRefresh(dir, host, repo string) {
	interval := s.Refresh
	if interval <= 0 {
		interval = time.Minute
	}
	if b, err := os.ReadFile(dir + "/.artifact-fetched"); err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); err == nil {
			if time.Since(t) < interval {
				return
			}
		}
	}
	if !s.beginRefresh(host + "/" + repo) {
		return
	}
	go func() {
		defer s.endRefresh(host + "/" + repo)
		mu := s.repoLock(host + "/" + repo)
		mu.Lock()
		defer mu.Unlock()
		upstream, err := s.upstreamURL(context.Background(), host, repo)
		if err != nil {
			return
		}
		tctx, cancel := context.WithTimeout(context.Background(), s.timeout())
		defer cancel()
		cmd := exec.CommandContext(tctx, "git", "--git-dir", dir, "remote", "set-url", "origin", upstream)
		cmd.Env = gitEnv()
		if err := cmd.Run(); err != nil {
			return
		}
		cmd = exec.CommandContext(tctx, "git", "--git-dir", dir, "fetch", "--prune", "origin",
			"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
		cmd.Env = gitEnv()
		if err := cmd.Run(); err == nil {
			_ = os.WriteFile(dir+"/.artifact-fetched", []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
		}
	}()
}

// beginRefresh marks a repository as refreshing; false means one is running.
func (s *State) beginRefresh(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshing == nil {
		s.refreshing = map[string]bool{}
	}
	if s.refreshing[key] {
		return false
	}
	s.refreshing[key] = true
	return true
}

func (s *State) endRefresh(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refreshing, key)
}

// upstreamURL is where the bare mirror is cloned/fetched from.
func (s *State) upstreamURL(ctx context.Context, host, repo string) (string, error) {
	override, _ := s.Registry.Upstreams.RepoOverride("git", host+"/"+repo)
	if override.Base != "" {
		return override.Base, nil
	}
	// Any syntactically valid public host may be mirrored: the host alone
	// identifies the git server, so a dotted hostname is accepted as https.
	// IsPublicHostname is the SSRF guard (no cluster-local names, no bare
	// addresses).
	if easyblobkit.IsPublicHostname(host) {
		return "https://" + host + "/" + repo, nil
	}
	return "", &gitError{Op: "upstream", Err: errUnknownHost{host}}
}

// selfBase is the external base URL the client used, for emitted LFS URLs.
func (s *State) selfBase(r *http.Request) string {
	if s.SelfBase != "" {
		return s.SelfBase
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// gitError carries the command output so the gateway surfaces why a mirror
// could not be built.
type gitError struct {
	Op  string
	Err error
	Out string
	// NotFound is set when the upstream authoritatively has no such repository
	// (a 404), as opposed to a transient/network/auth failure. Callers map it to
	// a 404 so a client can tell "repo absent" from "mirror broken".
	NotFound bool
}

func (e *gitError) Error() string {
	out := strings.TrimSpace(e.Out)
	if len(out) > 400 {
		out = out[:400] + "..."
	}
	if out == "" {
		return "git " + e.Op + ": " + e.Err.Error()
	}
	return "git " + e.Op + ": " + e.Err.Error() + ": " + out
}

// upstreamNotFound reports whether a `git clone` failure output says the
// repository does not exist upstream (a definitive 404, not a transport error).
// GitHub/Gitea say "Repository not found"; a generic server may say "not found".
func upstreamNotFound(out string) bool {
	o := strings.ToLower(out)
	return strings.Contains(o, "repository not found") ||
		strings.Contains(o, "not found") ||
		strings.Contains(o, "does not exist")
}

type errUnknownHost struct{ host string }

func (e errUnknownHost) Error() string { return "unknown git host: " + e.host }

// isGitDir reports whether p looks like a bare git repository.
func isGitDir(p string) bool {
	for _, f := range []string{"HEAD", "objects", "refs"} {
		if _, err := os.Stat(filepath.Join(p, f)); err != nil {
			return false
		}
	}
	return true
}

// gitEnv is a non-interactive, credentials-free environment for the git CLI.
// The upstream proxy (mihomo) comes from the inherited HTTP(S)_PROXY.
func gitEnv() []string {
	env := os.Environ()
	env = append(env,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GCM_INTERACTIVE=never",
	)
	return env
}
