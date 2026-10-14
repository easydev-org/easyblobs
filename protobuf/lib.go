// Package protobuf implements a pull-through proxy for the Buf Schema Registry
// (BSR) API, cached in the CAS and served verbatim.
//
// The BSR is NOT a REST registry: it is a set of Protobuf RPC services exposed
// over Connect (and gRPC / gRPC-Web). Every method is mounted at the host root:
//
//	POST https://buf.build/<fully.qualified.ServiceName>/<MethodName>
//	  Content-Type: application/json   (Connect JSON encoding)
//	  Authorization: Bearer <token>    (private modules)
//
// The main surface is the module `buf.build/bufbuild/registry`
// (`buf.registry.module.v1.ModuleService` and friends). See
// https://buf.build/docs/bsr/apis/api-access/.
//
// Mounted here as a transparent passthrough of that root:
//
//	POST /artifacts/protobuf/<fully.qualified.ServiceName>/<MethodName>
//
// so a `buf` client (or any Connect client) can point its BSR base URL at
// artifact instead of https://buf.build. Bodies are cached by (service,
// method, request body); the Authorization header is forwarded upstream.
//
// NOTE: the BSR "generated SDK" registries are served over the normal package
// protocols, not this one — Go SDKs (buf.build/gen/go/...) go through the Go
// proxy, npm SDKs (@buf/...) through the npm registry.
package protobuf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	// BSRToken, when set, is sent upstream as `Authorization: Bearer <token>`
	// (required for private BSR instances/modules). A caller's own
	// Authorization header is always forwarded in preference.
	BSRToken string
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["bsr_token"].(string); ok {
		s.BSRToken = v
	}
	return s, nil
}

func init() { easyblobkit.Register("protobuf", NewHandler) }

// maxBSRBody bounds a request body (BSR request messages are small).
const maxBSRBody = 1 << 20

// ServeHTTP proxies a BSR Connect call:
//
//	POST /artifacts/protobuf/<Service>/<Method>
//
// The whole path after the mount is the fully-qualified service + method, which
// is exactly the upstream path (methods live at the host root).
func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// Connect unary calls are POSTs; anything else is not a BSR method.
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/protobuf")
	path = strings.Trim(path, "/")
	if path == "" {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBSRBody))
	if err != nil {
		easyblobkit.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	// The response depends on the service+method AND the request body.
	cacheKey := path + "?" + hashHex(body)
	if art, gerr := s.Registry.Meta.Get(r.Context(), "protobuf", path, cacheKey); gerr == nil && len(art.Blobs) > 0 {
		if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), art.Blobs[0].Digest, "application/json") {
			return
		}
	}
	base := s.Registry.Upstreams.Get("protobuf")
	if base == "" {
		easyblobkit.Error(w, http.StatusNotFound, "protobuf upstream disabled (air-gap)")
		return
	}
	up, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: strings.TrimSuffix(base, "/")})
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream: "+err.Error())
		return
	}
	// Prefer the caller's Authorization (their BSR token); else the configured one.
	if auth := r.Header.Get("Authorization"); auth != "" {
		up = up.WithHeader("Authorization", auth)
	} else if s.BSRToken != "" {
		up = up.WithHeader("Authorization", "Bearer "+s.BSRToken)
	}
	accept := r.Header.Get("Accept")
	if accept == "" {
		accept = "application/json"
	}
	resp, err := up.Do(r.Context(), http.MethodPost, "/"+path, bytes.NewReader(body), "application/json", accept)
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream fetch: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "upstream read: "+err.Error())
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		storeCache(s, r.Context(), path, cacheKey, data)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(data)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// storeCache persists a fetched response (best-effort).
func storeCache(s *State, ctx context.Context, repo, version string, data []byte) {
	stored, err := s.Registry.StoreAndHash(ctx, data)
	if err != nil {
		return
	}
	easyblobkit.LogMetaErr("protobuf cache", s.Registry.Meta.Put(ctx, easyblobkit.Artifact{
		Format: "protobuf", Repository: repo, Version: version,
		MediaType: "application/json", Digest: stored.Digest,
		Blobs:  []easyblobkit.Descriptor{{Digest: stored.Digest, Size: stored.Size, Name: version}},
		Source: "pull",
	}))
}
