package easyblobkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"

	"golang.org/x/sync/singleflight"
)

// fetchGroup collapses concurrent identical upstream fetches process-wide: N
// simultaneous misses for one URL trigger a single upstream request, so a burst
// of clients (or a slow fetch) never downloads the same object N times. The key
// includes a digest of the request headers, so two callers with different
// credentials (a passthrough target) never share a result.
var fetchGroup singleflight.Group

// flightKey identifies one upstream fetch: the operation kind, the absolute URL,
// and a digest of the resolved request headers (Authorization in practice). The
// kind keeps callers with different result types (a []byte document vs a
// Fetched blob) from colliding on one in-flight entry.
func flightKey(kind string, remote *Remote, path string) string {
	url := kind + "\x00" + remote.URL(path)
	if len(remote.headers) == 0 {
		return url
	}
	keys := make([]string, 0, len(remote.headers))
	for k := range remote.headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = io.WriteString(h, k)
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, remote.headers[k])
		_, _ = io.WriteString(h, "\x00")
	}
	return url + "\x00" + hex.EncodeToString(h.Sum(nil))
}

// Fetch kinds for the single-flight namespace.
const (
	flightDoc      = "doc"      // GetBytes/GetBytesFollow: []byte
	flightFetched  = "fetched"  // fetchWith/FetchPathFollow: Fetched
	flightBlob     = "blob"     // FetchToBlob: blobResult
	flightPathBlob = "pathblob" // FetchCachedPath: pathCacheResult
)

// Remote is a proxy-aware upstream root URL. Adapters map their URL layout
// onto Get/GetBytes/GetCached.
type Remote struct {
	Base    string
	client  *http.Client
	headers map[string]string
}

// NewRemote creates a Remote for a base URL (trailing slash trimmed).
func NewRemote(factory *ClientFactory, base string, proxy *string) *Remote {
	return &Remote{Base: trimSlash(base), client: factory.Client(proxy)}
}

// WithHeader returns a copy with an extra header (e.g. Accept, auth).
func (r *Remote) WithHeader(k, v string) *Remote {
	cp := *r
	cp.headers = map[string]string{}
	for a, b := range r.headers {
		cp.headers[a] = b
	}
	cp.headers[k] = v
	return &cp
}

// URL joins a path onto the base.
func (r *Remote) URL(path string) string { return r.Base + path }

// Do issues an arbitrary request against the remote root. body may be nil; a
// non-empty contentType is set as the request Content-Type. It is the general
// escape hatch for protocols (git smart-HTTP) whose verbs are not plain GET.
func (r *Remote) Do(ctx context.Context, method, path string, body io.Reader, contentType, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, r.URL(path), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	return r.client.Do(req)
}

// Get issues GET and returns the raw response (non-2xx returned as-is).
func (r *Remote) Get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL(path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	return r.client.Do(req)
}

// GetBytes GETs and errors on non-2xx, returning the body bytes. Transient
// failures — transport errors or 502/503/504 responses (an egress proxy often
// surfaces upstream blips that way) — are retried: these are idempotent
// index/file GETs, and short backoff rides out multi-second proxy flaps.
//
// Concurrent identical requests are collapsed by a process-wide single-flight
// group (keyed by URL + auth headers), so N clients resolving the same index
// document cause one upstream fetch.
func (r *Remote) GetBytes(ctx context.Context, path string) ([]byte, error) {
	v, err, _ := fetchGroup.Do(flightKey(flightDoc, r, path), func() (any, error) {
		return r.getBytesDirect(ctx, path)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// getBytesDirect is GetBytes without the single-flight wrapper.
func (r *Remote) getBytesDirect(ctx context.Context, path string) ([]byte, error) {
	resp, err := r.getStable(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &UpstreamStatusError{Path: path, Status: resp.StatusCode}
	}
	dl := activeDownloads.begin(path, "", false, resp.ContentLength)
	defer activeDownloads.end(dl)
	return io.ReadAll(&countingReader{r: resp.Body, e: dl})
}

// UpstreamStatusError is the shared non-2xx upstream error.
type UpstreamStatusError struct {
	Path   string
	Status int
}

func (e *UpstreamStatusError) Error() string {
	return fmt.Sprintf("upstream %s: status %d", e.Path, e.Status)
}
