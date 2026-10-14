package git

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// infoRefs serves the ref advertisement for upload-pack (fetch) or
// receive-pack (push). Fetch reads it from the local mirror when one exists.
func (s *State) infoRefs(w http.ResponseWriter, r *http.Request, host, repo string) {
	service := r.URL.Query().Get("service")
	if service != "git-upload-pack" && service != "git-receive-pack" {
		s.proxy(w, r, host, repo+"/info/refs?"+r.URL.RawQuery)
		return
	}
	if service == "git-receive-pack" && !easyblobkit.AuthorizeWriteFor(w, r, s.Auth, s.Registry, "git", repo) {
		return
	}
	dir, err := s.ensureMirror(r.Context(), host, repo)
	if err != nil {
		s.mirrorErr(w, err)
		return
	}
	if dir == "" { // local storage disabled: proxy the discovery
		s.proxy(w, r, host, repo+"/info/refs?"+r.URL.RawQuery)
		return
	}
	// Allow an annotated serving of the advertisement via the git CLI.
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	writePktLine(w, "# service="+service+"\n")
	_, _ = io.WriteString(w, "0000") // flush, then the advertisement
	s.runService(w, r, dir, service, true)
}

// service handles the POST body of upload-pack / receive-pack against the
// local mirror (stateless RPC).
func (s *State) service(w http.ResponseWriter, r *http.Request, host, repo, service string, write bool) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if write && !easyblobkit.AuthorizeWriteFor(w, r, s.Auth, s.Registry, "git", repo) {
		return
	}
	dir, err := s.ensureMirror(r.Context(), host, repo)
	if err != nil {
		s.mirrorErr(w, err)
		return
	}
	if dir == "" {
		s.proxy(w, r, host, repo+"/"+service)
		return
	}
	easyblobkit.LimitBody(w, r)
	w.Header().Set("Content-Type", "application/x-git-"+service+"-result")
	w.Header().Set("Cache-Control", "no-cache")
	s.runService(w, r, dir, service, false)
}

// runService executes `git <service> --stateless-rpc` against the mirror,
// streaming the request body in and the result out. advertise adds the
// --advertise-refs flag (discovery).
func (s *State) runService(w http.ResponseWriter, r *http.Request, dir, service string, advertise bool) {
	// service is "git-upload-pack"/"git-receive-pack"; the CLI subcommand is
	// the bare "upload-pack"/"receive-pack".
	sub := strings.TrimPrefix(service, "git-")
	args := []string{sub, "--stateless-rpc"}
	if advertise {
		args = append(args, "--advertise-refs")
	}
	args = append(args, dir)
	cmd := exec.CommandContext(r.Context(), "git", args...)
	cmd.Env = gitEnv()
	if !advertise {
		var body []byte
		if r.Body != nil {
			body, _ = requestBody(r, maxBody)
		}
		cmd.Stdin = bytes.NewReader(body)
	}
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The head is already written; append the error for diagnostics only.
		_, _ = io.WriteString(w, "\n# git "+service+" error: "+err.Error()+" "+stderr.String())
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// writePktLine writes one git pkt-line ("<4-hex-len><payload>").
func writePktLine(w io.Writer, payload string) {
	_, _ = io.WriteString(w, formatPktLen(len(payload)+4)+payload)
}

// mirrorErr maps a mirror-build failure onto a client response. When the
// upstream authoritatively has no such repository, return 404 — not a 502 with
// a misleading "Authentication failed" body — so a client (SPM, git) can tell
// "repo absent" from "mirror broken" and fall back or fail cleanly.
func (s *State) mirrorErr(w http.ResponseWriter, err error) {
	var ge *gitError
	if errors.As(err, &ge) && ge.NotFound {
		easyblobkit.Error(w, http.StatusNotFound, "repository not found upstream")
		return
	}
	easyblobkit.Error(w, http.StatusBadGateway, "upstream: "+err.Error())
}

// proxy streams a non-smart request (dumb objects, HEAD, LFS batch) upstream.
func (s *State) proxy(w http.ResponseWriter, r *http.Request, host, path string) {
	var body []byte
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		easyblobkit.LimitBody(w, r)
		body, _ = requestBody(r, maxBody)
	}
	base := "https://" + host
	remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: base})
	resp, err := remote.Do(r.Context(), r.Method, "/"+strings.TrimPrefix(path, "/"), bytes.NewReader(body), r.Header.Get("Content-Type"), r.Header.Get("Accept"))
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// formatPktLen renders a git pkt-line length prefix (4 lowercase hex digits).
func formatPktLen(n int) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[(n>>12)&0xf], hexd[(n>>8)&0xf], hexd[(n>>4)&0xf], hexd[n&0xf]})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
