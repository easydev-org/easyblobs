package easyblobkit

import (
	"net/http"
	"net/url"
	"sync"
	"time"
)

// UserAgent is sent on every upstream request. Registries rate-limit generic
// user agents (Maven Central 429s the default Go UA), so a stable bespoke one
// is used.
const UserAgent = "easyblobkit/1.0 (pull-through mirror)"

// ClientFactory builds (and caches) an http.Client for a proxy policy:
//   - nil           -> follow the environment proxy configuration
//   - Some("")      -> direct connection (env proxy bypassed)
//   - Some(url)     -> always route through the given proxy
//
// One factory is shared process-wide (sharedFactory) so every upstream fetch
// reuses the same transports and keep-alive pool; building a factory per fetch
// would open a fresh TCP+TLS connection every time. It is safe for concurrent
// use.
type ClientFactory struct {
	mu      sync.Mutex
	clients map[string]*http.Client
}

// NewClientFactory returns an empty factory. Callers that fetch repeatedly
// should share one (see sharedFactory); this constructor is for tests and
// isolated embedders.
func NewClientFactory() *ClientFactory {
	return &ClientFactory{clients: map[string]*http.Client{}}
}

// Client returns a cached client for the proxy policy that does NOT follow
// redirects. Adapters use it when a 3xx must be surfaced to the caller (e.g.
// HuggingFace resolve → CDN, or a Debian mirror redirect) so the client
// decides: transparent pass-through, or an explicit re-fetch. Silently
// following such a redirect would download the whole object server-side and
// defeat the range/caching policy.
func (f *ClientFactory) Client(proxy *string) *http.Client {
	key := proxyKey(proxy)
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[key]; ok {
		return c
	}
	c := &http.Client{
		Transport: newTransport(proxy),
		// No overall Timeout: large blobs stream for minutes.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	f.clients[key] = c
	return c
}

// Redirecting returns a cached client for the proxy policy that FOLLOWS
// redirects (Go's default policy: up to 10 hops). It is used by registries
// whose blob endpoint 307-redirects to a CDN (Docker Hub → CloudFront, MCR →
// Azure Blob, GHCR/Quay): the adapter must read the object through the
// redirect to verify and cache it server-side.
//
// Cross-host redirects drop the Authorization header automatically (Go's
// shouldCopyHeaderOnRedirect), which is exactly what a presigned CDN URL
// needs; same-host redirects keep it.
func (f *ClientFactory) Redirecting(proxy *string) *http.Client {
	key := "__follow__" + proxyKey(proxy)
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[key]; ok {
		return c
	}
	c := &http.Client{Transport: newTransport(proxy)}
	f.clients[key] = c
	return c
}

// sharedFactory is the process-wide factory every Registry fetch uses, so
// upstream connections are pooled instead of re-dialed per request.
var sharedFactory = NewClientFactory()

// SharedClientFactory returns the process-wide client factory.
func SharedClientFactory() *ClientFactory { return sharedFactory }

func proxyKey(proxy *string) string {
	if proxy == nil {
		return "__env__"
	}
	return *proxy
}

// newTransport builds the shared HTTP transport for a proxy policy.
func newTransport(proxy *string) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 20
	// Cap concurrent connections to one upstream host so a burst of clients
	// pulling large blobs cannot exhaust file descriptors (or the egress
	// proxy's connection budget).
	tr.MaxConnsPerHost = 32
	tr.IdleConnTimeout = 90 * time.Second
	tr.ResponseHeaderTimeout = 120 * time.Second
	if proxy != nil {
		if *proxy == "" {
			tr.Proxy = nil // direct
		} else if u, err := url.Parse(*proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		} else {
			// A malformed proxy URL must not crash the process. Fall back to
			// the environment proxy and surface the misconfiguration once.
			LogMetaErr("invalid proxy url "+*proxy, err)
		}
	}
	return tr
}
