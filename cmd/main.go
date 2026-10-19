// Command artifact runs a multi-protocol package registry. Each protocol is a
// separate module that registers itself with easyblobkit at compile time (see the
// blank imports in adapters). The server can mount a single protocol or many,
// so a pull-through mirror may be started per-protocol or all-at-once.
//
// Example:
//
//	artifact server --protocols=oci,pypi --listen :8080 --data ./data
//	artifact server --protocols=oci --oci.upstream https://registry-1.docker.io
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/easydev-org/easyblobs/cmd/adapters" // registers all enabled protocols
	"github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
	"github.com/easydev-org/easyblobs/targets"
)

func main() {
	var (
		listen        = flag.String("listen", ":8080", "HTTP listen address")
		dataDir       = flag.String("data", "./data", "substrate root (sqlite + blobs + upstreams)")
		protocols     = flag.String("protocols", "", "comma-separated protocols to mount (default: all registered)")
		selfBase      = flag.String("self-base", "", "external base URL for auth realms / self URIs")
		tokens        = flag.String("tokens", "", "`token=level` pairs (read|write) to seed into the credential DB (hashed), comma separated; repeat on every start to keep them registered")
		airGap        = flag.Bool("air-gap", false, "disable all upstream pull-through")
		blobBackend   = flag.String("blob-backend", "filesystem", "blob backend: filesystem | s3")
		upstreamSet   = flag.String("upstreams", "", "override upstream base for a format, `format=url` pairs comma separated (e.g. go=http://proxy.golang.org)")
		repoUpstreams = flag.String("repo-upstreams", "", "per-repository upstream overrides, `format/repo=url` pairs comma separated; the longest matching repo prefix wins (e.g. maven/org.apache=https://mirror.example/m2,npm/@acme=https://npm.example)")
		upstreamProxy = flag.String("upstream-proxy", "", "HTTP proxy URL for upstream fetches (empty = follow env, \"none\" = direct)")
		gcInterval    = flag.Duration("gc-interval", time.Hour, "background reaper period (expired negative entries + orphan blobs); 0 disables")
		gcGrace       = flag.Duration("gc-grace", time.Hour, "only orphan blobs older than this are removed (protects in-flight writes)")
	)
	flag.Parse()

	upstreams := defaultUpstreams(*airGap)
	applyUpstreamOverrides(upstreams, *upstreamSet, *upstreamProxy)
	applyRepoUpstreams(upstreams, *repoUpstreams)
	// Open the metadata store on the configured backend (sqlite by default;
	// postgres/mysql via EASYBLOBS_DB_DRIVER/DSN). Blob backend is chosen below.
	meta, err := openMeta(*dataDir)
	if err != nil {
		log.Fatalf("open metadata: %v", err)
	}
	defer func() { _ = meta.Close() }()

	// Auth backend: static (default; --tokens seeds the credential DB, no users
	// ⇒ open instance) or easyvcs delegation (EASYBLOBS_AUTH=easyvcs). See
	// selectAuth.
	auth := selectAuth(meta, *tokens)

	var blobs easyblobkit.BlobStore
	// Blob backend: filesystem (default) via the factory, or S3 placeholder. The
	// inline-SQLite blob backend is removed; artifact bytes live on the filesystem.
	blobs, err = store.OpenBlobStore(*blobBackend, filepath.Join(*dataDir, "blobs"))
	if err != nil {
		log.Fatalf("open blob store: %v", err)
	}
	// OCI upload staging follows the blob backend: filesystem -> local files,
	// s3 -> the same bucket, so upload state does not live on the local disk.
	uploads, err := easyblobkit.OpenUploadStore(*blobBackend, filepath.Join(*dataDir, "oci-uploads"))
	if err != nil {
		log.Fatalf("open upload store: %v", err)
	}

	reg := &easyblobkit.Registry{Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta), Upstreams: upstreams, TargetStore: meta}
	// Load user-declared targets from the metadata DB on top of the built-ins.
	// A load failure must not prevent startup: the built-in table is a working
	// registry, and a broken row is dropped by the store.
	if err := upstreams.Targets.Load(context.Background(), meta); err != nil {
		log.Printf("targets: load user targets: %v", err)
	}

	// Build one handler per enabled protocol and mount it at /artifacts/<proto>.
	// Only a protocol's DEFAULT target is a mount; a named mirror (maven.google)
	// is reached via /artifacts/<proto>/_upstream/<host>/... instead (the scope
	// middleware records the explicit upstream), so adapters never see which
	// mirror served a request.
	names := *protocols
	enabled := map[string]bool{}
	if names == "" {
		for _, n := range easyblobkit.Registered() {
			enabled[n] = true
		}
	} else {
		for _, n := range strings.Split(names, ",") {
			if n = strings.TrimSpace(n); n != "" {
				enabled[n] = true
			}
		}
	}
	mux := http.NewServeMux()
	mounted := 0
	handlers := map[string]http.Handler{}
	for proto := range enabled {
		// Build one handler per protocol (its targets share it).
		if !easyblobkit.ProtocolRegistered(proto) {
			log.Fatalf("build protocol %q: unknown protocol", proto)
		}
		handler, err := easyblobkit.Build(proto, reg, configFor(proto, *selfBase, auth, *dataDir, meta, blobs, uploads))
		if err != nil {
			log.Fatalf("build protocol %q: %v", proto, err)
		}
		if proto == "oci" {
			// OCI is spec-fixed at /v2 and its registry is the request Host.
			if auth != nil {
				tokenH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					serveToken(w, r, auth)
				})
				mux.Handle("/v2/token", tokenH)
				mux.Handle("/token", tokenH)
			}
			scoped := easyblobkit.ScopeMiddlewareForFormat("oci", "/v2", handler)
			mux.Handle("/v2", scoped)
			mux.Handle("/v2/", scoped)
			mounted++
			continue
		}
		handlers[proto] = handler
	}
	if len(handlers) > 0 {
		// One dispatcher serves every target under /artifacts/<target-id>/...,
		// resolving the target per request so a target created through the
		// admin API is immediately reachable. A protocol's default target has
		// the protocol name as its ID, so /artifacts/<protocol> is unchanged.
		d := easyblobkit.NewTargetDispatcher(easyblobkit.MountBase, reg, handlers)
		mux.Handle(easyblobkit.MountBase+"/", d)
		mux.Handle(easyblobkit.MountBase, d)
		mounted += len(handlers)
	}
	if mounted == 0 {
		log.Fatal("no protocols registered/enabled")
	}

	// Health endpoints (unauthenticated, outside the protocol mounts):
	//   /healthz  liveness  — the process is up (always 200).
	//   /readyz   readiness — the metadata store answers a cheap query; a
	//                         failing DB (locked, gone) pulls the pod from the
	//                         Service instead of serving errors.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok "+appVersion()+"\n")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := meta.ListRepositories(r.Context()); err != nil {
			http.Error(w, "metadata store unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ready "+appVersion()+"\n")
	})

	addr := *listen
	log.Printf("easyblobs listening on %s (%d protocols: %s), data=%s, airgap=%v",
		addr, mounted, names, *dataDir, *airGap)
	// Background reaper: expire negative cache entries and reclaim orphan
	// blobs. -gc-interval=0 disables it.
	go store.RunReaper(context.Background(), meta, blobs, *gcInterval, *gcGrace, log.Printf)
	// EASYBLOBS_DEBUG=1 logs every request (method, path, framing, status) and
	// is invaluable when a client's upload/download framing is in question.
	debug := os.Getenv("EASYBLOBS_DEBUG") != ""
	var root http.Handler = cleanPaths(mux)
	if debug {
		root = debugHandler(root)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // large layer uploads stream slowly
		WriteTimeout:      10 * time.Minute, // large layer downloads stream slowly
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// cleanPaths canonicalizes the request path before routing. Go's ServeMux
// already issues a 301 for paths that need cleaning, but clients such as apt
// do not always follow that redirect for their per-line base URLs (they send
// "/repo/./Packages" with a literal "." segment). Cleaning first keeps apt's
// flat-repo requests on the same route as a normal fetch.
func cleanPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "" && strings.Contains(r.URL.Path, "/.") {
			if u := *r.URL; true {
				u.Path = path.Clean(u.Path)
				if u.RawPath != "" {
					u.RawPath = path.Clean(u.RawPath)
				}
				r2 := new(http.Request)
				*r2 = *r
				r2.URL = &u
				r = r2
			}
		}
		next.ServeHTTP(w, r)
	})
}

// debugHandler logs each request's method, path, body framing and the
// resulting status when EASYBLOBS_DEBUG is set.
func debugHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &logResponseWriter{ResponseWriter: w, status: 200}
		var body counter
		if r.Body != nil {
			r.Body = &countingBody{ReadCloser: r.Body, n: &body}
		}
		next.ServeHTTP(lw, r)
		cl := r.Header.Get("Content-Length")
		if cl == "" {
			cl = "-"
		}
		log.Printf("req %s %s cl=%s te=%q ct=%q body=%d dedic=%q sha1=%q -> %d (%s)",
			r.Method, r.URL.Path, cl, r.TransferEncoding, r.Header.Get("Content-Type"),
			body.n, r.Header.Get("X-Checksum-Deploy"), r.Header.Get("X-Checksum-Sha1"),
			lw.status, time.Since(start).Round(time.Millisecond))
	})
}

type counter struct{ n int64 }

type countingBody struct {
	io.ReadCloser
	n *counter
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.n += int64(n)
	return n, err
}

type logResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *logResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// defaultUpstreams builds the upstream table from the target registry, which is
// the single source of truth for public upstreams and mirrors. The flat
// Defaults map is a projection of the registry so existing resolvers, the admin
// API and per-deployment overrides keep working unchanged.
func defaultUpstreams(airGap bool) *easyblobkit.Upstreams {
	reg := targets.NewRegistry()
	return &easyblobkit.Upstreams{
		Targets:   reg,
		Defaults:  reg.LegacyDefaults(),
		Overrides: map[string]string{},
		Proxy:     map[string]string{},
		AirGap:    airGap,
	}
}

// applyUpstreamOverrides mutates the upstream table from flag input:
//   - overrides: comma-separated "format=url" pairs replacing a format's base
//     (also usable to add a non-default format key).
//   - proxy: an HTTP proxy URL applied to every format's upstream fetches
//     (mihomo in the dev cluster); "none" forces direct connections.
func applyUpstreamOverrides(u *easyblobkit.Upstreams, overrides, proxy string) {
	for _, pair := range strings.Split(overrides, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		u.Overrides[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if proxy == "" {
		return
	}
	if u.Proxy == nil {
		u.Proxy = map[string]string{}
	}
	if strings.EqualFold(proxy, "none") {
		u.Proxy["*"] = ""
		return
	}
	for k := range u.Defaults {
		u.Proxy[k] = proxy
	}
	u.Proxy["*"] = proxy
}

func configFor(name, selfBase string, auth easyblobkit.Auth, dataDir string, meta *store.Store, blobs easyblobkit.BlobStore, uploads easyblobkit.UploadStore) map[string]any {
	cfg := map[string]any{}
	// Each protocol mounts under /artifacts/<name> (OCI is special-cased to
	// /v2), so its emitted self-URLs must carry that prefix. selfBase is the
	// global origin (scheme://host[:port]) the client reached the gateway by.
	// For OCI, SelfBase is used ONLY to derive the /token realm, which lives
	// at the origin root — so pass the bare base.
	if selfBase != "" {
		if name == "oci" {
			cfg["self_base"] = strings.TrimSuffix(selfBase, "/")
		} else {
			cfg["self_base"] = strings.TrimSuffix(selfBase, "/") + "/artifacts/" + name
		}
	}
	if auth != nil {
		cfg["auth"] = auth
	}
	if name == "oci" {
		// Upload staging is the backend-selected store (local files for the
		// filesystem backend, the S3 bucket for s3) — not the local data dir
		// when blobs are remote.
		cfg["uploads"] = uploads
	}
	if name == "system" && meta != nil {
		// The admin API can CRUD user-declared targets and report footprint.
		cfg["targets_store"] = meta
		cfg["stats_func"] = func(ctx context.Context) (any, error) {
			return meta.Stats(ctx, blobs)
		}
	}
	return cfg
}

// serveToken issues an OCI bearer token from the configured auth. Pull-only
// scopes are granted to anyone (an anonymous pull token carries no write
// privilege); push/delete scopes require an authenticated write-level
// principal, and the minted token never exposes the static credential.
func serveToken(w http.ResponseWriter, r *http.Request, auth easyblobkit.Auth) {
	// Support both the GET token flow (scope in the query, credentials in
	// headers) and the OAuth2 password-grant POST (form body) used by
	// buildkit/containerd push.
	_ = r.ParseForm()
	scopeVals := append([]string{}, r.URL.Query()["scope"]...)
	scopeVals = append(scopeVals, r.PostForm["scope"]...)
	scopes := collectScopes(scopeVals)
	username := auth.Authenticate(r.Context(), r)
	if username == "" {
		// Password carries the credential (same as Basic); refresh_token may
		// carry a raw token.
		if pass := r.PostForm.Get("password"); pass != "" {
			if u, ok := auth.CheckToken(r.Context(), pass); ok {
				username = u
			}
		} else if ref := r.PostForm.Get("refresh_token"); ref != "" {
			if u, ok := auth.CheckToken(r.Context(), ref); ok {
				username = u
			}
		}
	}
	if username == "" && canPush(scopes) {
		// Anonymous push scope: challenge rather than mint anything.
		w.Header().Set("WWW-Authenticate", `Basic realm="/token"`)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"errors": []any{
			map[string]any{"code": "UNAUTHORIZED", "message": "authentication required"},
		}})
		return
	}
	tok := auth.IssueToken(r.Context(), username, scopes, time.Hour)
	if tok == "" && canPush(scopes) {
		// The auth layer refused to mint (e.g. a read-level principal asking
		// for push): the credential is valid but not privileged enough.
		w.Header().Set("WWW-Authenticate", `Basic realm="/token"`)
		writeJSON(w, http.StatusForbidden, map[string]any{"errors": []any{
			map[string]any{"code": "DENIED", "message": "insufficient privilege for requested scope"},
		}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok, "access_token": tok, "expires_in": 3600,
	})
}

func collectScopes(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, s := range strings.Fields(v) {
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func canPush(scopes []string) bool {
	for _, s := range scopes {
		if i := strings.LastIndex(s, ":"); i >= 0 {
			act := s[i+1:]
			// An OCI scope may carry comma-separated actions ("pull,push").
			for _, a := range strings.Split(act, ",") {
				if a == "push" || a == "delete" || a == "*" {
					return true
				}
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "%s", mustJSON(v))
}

func mustJSON(v any) string {
	b, err := jsonMarshal(v)
	if err != nil {
		return `{}`
	}
	return string(b)
}

// seedTokens registers `token=level` pairs into the credential DB. Idempotent:
// a token already present (same hash) is not duplicated.
func seedTokens(meta *store.Store, spec string) error {
	ctx := context.Background()
	for _, pair := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' }) {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("malformed token pair %q (want token=level)", pair)
		}
		tok, level := parts[0], parts[1]
		switch level {
		case "read", "r", "rl":
			level = "read"
		case "write", "w", "rw":
			level = "write"
		default:
			return fmt.Errorf("invalid level %q (read|write)", level)
		}
		if _, ok := meta.LookupToken(ctx, tok); ok {
			continue // already seeded
		}
		uid, err := meta.CreateAuthUser(ctx, "token-user")
		if err != nil {
			return err
		}
		if err := meta.CreateAuthToken(ctx, tok, uid, level); err != nil {
			return err
		}
		log.Printf("auth: seeded credential (level %s)", level)
	}
	return nil
}

// applyRepoUpstreams installs per-repository upstream overrides from
// "format/repo=url" pairs. The repository part may be a namespace prefix
// (maven/org.apache, npm/@acme, go/github.com/acme); the longest matching
// prefix wins at request time, so one entry covers a whole subtree.
func applyRepoUpstreams(u *easyblobkit.Upstreams, pairs string) {
	for _, pair := range strings.Split(pairs, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		format, repo, ok := strings.Cut(k, "/")
		if !ok || format == "" || repo == "" || v == "" {
			continue
		}
		u.SetRepo(format, repo, v, "")
	}
}
