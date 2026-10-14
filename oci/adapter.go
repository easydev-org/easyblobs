package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

// OciState configures the OCI adapter.
type OciState struct {
	// Registry is the substrate (metadata + blob + upstreams).
	Registry *easyblobkit.Registry
	// Auth is optional; nil disables auth (anonymous pull-only).
	Auth easyblobkit.Auth
	// DefaultUpstream is the pull-through base (scheme://host). Empty "https://registry-1.docker.io".
	DefaultUpstream string
	// SelfBase is the external base URL (scheme://host[:port]) used for auth
	// challenge realms and absolute self URIs in responses.
	SelfBase string
	// Uploads stages in-progress blob uploads. It is chosen by the blob
	// backend (filesystem -> local files, s3 -> the same bucket as the CAS) so
	// OCI upload state can live off the local disk. Nil falls back to a local
	// store at <TempDir>/artifact-oci-uploads.
	Uploads easyblobkit.UploadStore
}

// defaultManifestTTL is how long a pulled manifest is trusted when it was
// addressed by TAG. Tags move (latest, stable, a re-pushed version), so a
// cached tag row expires and is re-resolved; digest references name immutable
// content and are cached permanently.
const defaultManifestTTL = 5 * time.Minute

// Adapter is the OCI protocol handler. It implements http.Handler, so it can
// be mounted at any prefix (usually "/v2").
type Adapter struct {
	state *OciState
	// registryUpstreams is a per-host cache of on-demand upstreams for
	// explicitly prefixed registries (ghcr.io/...).
	registryUpstreamsMu sync.Mutex
	registryUpstreams   map[string]*Client
	// uploads stages in-progress blob uploads.
	uploads easyblobkit.UploadStore
	// sweeperStop stops the upload-session GC.
	sweeperStop chan struct{}
}

// New builds an OCI adapter. Upload staging is OciState.Uploads (when nil, a
// local store at <TempDir>/artifact-oci-uploads).
func New(state *OciState) *Adapter {
	if state == nil {
		state = &OciState{}
	}
	if state.DefaultUpstream == "" {
		state.DefaultUpstream = "https://registry-1.docker.io"
	}
	uploads := state.Uploads
	if uploads == nil {
		root := filepath.Join(os.TempDir(), "artifact-oci-uploads")
		store, err := easyblobkit.NewFileUploadStore(root)
		if err != nil {
			// A broken upload dir is a broken environment; surface it eagerly.
			panic("artifact/oci: upload sessions: " + err.Error())
		}
		uploads = store
	}
	a := &Adapter{state: state, registryUpstreams: map[string]*Client{}, uploads: uploads, sweeperStop: make(chan struct{})}
	go a.sweepLoop()
	return a
}

// sweepLoop periodically drops abandoned upload sessions (1h cadence, 24h
// max age).
func (a *Adapter) sweepLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			a.uploads.Sweep(context.Background(), 24*time.Hour)
		case <-a.sweeperStop:
			return
		}
	}
}

// Stop halts background maintenance (the sweep loop). Call on shutdown.
func (a *Adapter) Stop() {
	select {
	case <-a.sweeperStop:
	default:
		close(a.sweeperStop)
	}
}

// Name implements easyblobkit.Protocol.
func (a *Adapter) Name() string { return "oci" }

// NewHandler is the constructor signature for easyblobkit.Register.
func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	state := &OciState{Registry: reg}
	if v, ok := cfg["default_upstream"].(string); ok && v != "" {
		state.DefaultUpstream = v
	}
	if v, ok := cfg["self_base"].(string); ok && v != "" {
		state.SelfBase = v
	}
	if v, ok := cfg["uploads"].(easyblobkit.UploadStore); ok && v != nil {
		state.Uploads = v
	}
	if v, ok := cfg["auth"].(easyblobkit.Auth); ok {
		state.Auth = v
	}
	return New(state), nil
}

func (a *Adapter) selfBase() string {
	if a.state.SelfBase != "" {
		return strings.TrimSuffix(a.state.SelfBase, "/")
	}
	return "http://localhost:8080"
}

func (a *Adapter) tokenRealm() string {
	base := a.selfBase()
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return base + "/token"
	}
	return "http://" + base + "/token"
}

// upstreamForRegistry returns the pull-through upstream for a registry host
// ("" = default upstream; docker hub aliases -> default).
func (a *Adapter) upstreamForRegistry(host string) *Client {
	host = strings.ToLower(strings.TrimSpace(host))
	// An explicit repository override (-repo-upstreams oci/ghcr.io=...) wins
	// over the derived https://<host> default.
	if a.state.Registry != nil && a.state.Registry.Upstreams != nil {
		if e, ok := a.state.Registry.Upstreams.RepoOverride("oci", host); ok {
			var p *string
			if e.Proxy != "" {
				p = &e.Proxy
			}
			return NewClient(a.state.Registry.Upstreams.ProxyFactory(), e.Base, p)
		}
	}
	switch host {
	case "", "docker.io", "index.docker.io", "registry-1.docker.io":
		return NewClient(
			a.state.Registry.Upstreams.ProxyFactory(),
			a.state.DefaultUpstream,
			proxyOf(a.state.Registry.Upstreams, "oci"),
		)
	default:
		a.registryUpstreamsMu.Lock()
		defer a.registryUpstreamsMu.Unlock()
		if u, ok := a.registryUpstreams[host]; ok {
			return u
		}
		u := ForRegistry(
			a.state.Registry.Upstreams.ProxyFactory(),
			host,
			proxyOf(a.state.Registry.Upstreams, host),
		)
		a.registryUpstreams[host] = u
		return u
	}
}

func proxyOf(u *easyblobkit.Upstreams, key string) *string {
	if v, ok := u.ProxyURL(key); ok {
		return &v
	}
	return nil
}

// ServeHTTP dispatches every /v2/* path by method.
//
// A docker/podman client carries the target registry in TLS SNI and the Host
// header, never in the path (docker pull ghcr.io/acme/app sends
// "GET /v2/acme/app/..."). The adapter therefore reads the registry from the
// request Host and canonicalises docker hub's aliases (index.docker.io,
// registry-1.docker.io → docker.io) so every alias shares one repository. When
// the Host is the gateway itself (an in-cluster service name or an IP) the
// default upstream is used instead.
func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/v2")
	rel = strings.TrimPrefix(rel, "/")
	// The registry travels in the Host header. The scope middleware records it
	// for the mounted server; when the adapter is driven directly (tests) fall
	// back to the request Host so the same derivation applies.
	if easyblobkit.RegistryHostFrom(r.Context()) == "" {
		if h := easyblobkit.CanonicalHost(r.Host); easyblobkit.IsRegistryHost(h) {
			r = r.WithContext(easyblobkit.WithRegistryHost(r.Context(), h))
		}
	}

	switch {
	case rel == "" || rel == "/":
		a.ping(w, r)
	case rel == "_catalog":
		a.catalog(w, r)
	case strings.HasSuffix(rel, "/tags/list"):
		name := strings.TrimSuffix(rel, "/tags/list")
		a.listTags(w, r, name)
	case strings.Contains(rel, "/referrers/"):
		idx := strings.LastIndex(rel, "/referrers/")
		a.listReferrers(w, r, rel[:idx], rel[idx+len("/referrers/"):])
	case strings.Contains(rel, "/manifests/"):
		idx := strings.LastIndex(rel, "/manifests/")
		a.manifest(w, r, rel[:idx], rel[idx+len("/manifests/"):])
	case strings.Contains(rel, "/blobs/uploads/"):
		idx := strings.LastIndex(rel, "/blobs/uploads/")
		a.upload(w, r, rel[:idx], rel[idx+len("/blobs/uploads/"):])
	case strings.Contains(rel, "/blobs/"):
		idx := strings.LastIndex(rel, "/blobs/")
		a.blob(w, r, rel[:idx], rel[idx+len("/blobs/"):])
	default:
		writeJSON(w, http.StatusNotFound, ociError("MANIFEST_UNKNOWN", "manifest unknown"))
	}
}

// authorize checks the request's credential against the exact action being
// performed. Pull is public: any reader may fetch manifests/blobs without a
// credential (the token dance is still supported and honored for clients that
// perform it). Write actions (push/delete) always require a write-level
// credential.
func (a *Adapter) authorize(r *http.Request, name string, act easyblobkit.Action) bool {
	if a.state.Auth == nil {
		return true // auth disabled: anonymous full access (dev/open mode)
	}
	if act == easyblobkit.ActionPull {
		return true
	}
	scope := "repository:" + name + ":" + string(act)
	// Accept every scheme a client may present for a write: a Bearer token
	// (pushed through the /token realm), OR a static/Basic credential. This
	// keeps the Distribution token dance working while also letting tools like
	// skopeo/crane push directly with `-u user:token`.
	if easyblobkit.WriteCapable(r.Context(), a.state.Auth, r) {
		return true
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok != "" && tok != r.Header.Get("Authorization") {
		if _, ok := a.state.Auth.CheckBearer(r.Context(), tok, scope); ok {
			return true
		}
	}
	return false
}

// challenge emits a Distribution-spec Bearer auth challenge.
func (a *Adapter) challenge(w http.ResponseWriter, name string, act easyblobkit.Action) {
	scope := "repository:" + name + ":" + string(act)
	realm := a.tokenRealm()
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="oci-registry",scope="%s"`, realm, scope))
	writeJSON(w, http.StatusUnauthorized, ociError("UNAUTHORIZED", "authentication required"))
}

func (a *Adapter) ping(w http.ResponseWriter, r *http.Request) {
	// A registry with auth enabled MUST challenge on /v2/ so clients perform
	// the Distribution token flow (anonymous callers get a pull-only token).
	// Returning 200 here makes clients assume anonymous access and then fail
	// fatally on the first push (skopeo/crane never retry a write 401).
	if a.state.Auth != nil {
		realm := a.tokenRealm()
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s",service="oci-registry"`, realm))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
	w.WriteHeader(http.StatusOK)
}

// catalog lives in catalog.go (owner-scoped listing).
func (a *Adapter) listTags(w http.ResponseWriter, r *http.Request, name string) {
	if !a.authorize(r, name, easyblobkit.ActionPull) {
		a.challenge(w, name, easyblobkit.ActionPull)
		return
	}
	registry := easyblobkit.RegistryHostFrom(r.Context())
	tags, _ := a.state.Registry.Meta.ListVersions(r.Context(), "oci", name)
	set := map[string]bool{}
	for _, t := range tags {
		set[t] = true
	}
	if up := a.upstreamForRegistry(registry); up != nil {
		if utags, err := up.ListTags(name); err == nil {
			for _, t := range utags {
				if !set[t] {
					tags = append(tags, t)
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "tags": tags})
}

// --- helpers ----------------------------------------------------------------

func writeManifest(w http.ResponseWriter, body []byte, mt, dgst string, withBody bool) {
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Docker-Content-Digest", dgst)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if withBody {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	} else {
		w.WriteHeader(http.StatusOK)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ociError(code, msg string) map[string]any {
	return map[string]any{"errors": []any{map[string]any{"code": code, "message": msg, "detail": ""}}}
}

func hexDigest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
