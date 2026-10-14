package easyblobkit

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// retryBackoff spaces the retries shared by every pull-through path (getStable,
// getStreamRetry*). A cold-cache miss reaches the public upstream through the
// cluster egress proxy, where the FIRST fetch can 502 for several seconds while
// the proxy dials/caches — a two-attempt budget (~2s) turned that transient
// flap into a hard client failure. Five attempts over ~7.5s absorb it.
var retryBackoff = []time.Duration{
	500 * time.Millisecond,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
}

// jitter spreads a retry wait by ±25% so a burst of concurrent clients hitting
// the same cold upstream does not retry in lockstep (thundering herd).
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := int64(d) / 4
	if span <= 0 {
		return d
	}
	return d - time.Duration(span/2) + time.Duration(rand.Int63n(span))
}

// maxRetryAfter caps a server-directed Retry-After so a hostile or misconfigured
// origin cannot pin a request for minutes.
const maxRetryAfter = 30 * time.Second

// retryableStatus reports whether a response should be retried: transport
// errors (code 0), the classic egress-proxy hiccup codes 502/503/504, and 429
// (a rate limit) which Maven Central and other registries emit.
func retryableStatus(code int) bool {
	switch code {
	case 0, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, http.StatusTooManyRequests:
		return true
	}
	return false
}

// retryWait picks the backoff before the next attempt: the server's Retry-After
// when present (and sane), else the staged default. Retry-After may be a number
// of seconds or an HTTP date.
func retryWait(resp *http.Response, fallback time.Duration) time.Duration {
	if resp == nil {
		return fallback
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		if secs <= 0 {
			return fallback
		}
		if d := time.Duration(secs) * time.Second; d < maxRetryAfter {
			return d
		}
		return maxRetryAfter
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			if d > maxRetryAfter {
				return maxRetryAfter
			}
			return d
		}
	}
	return fallback
}

// getStable performs a GET, retrying transport errors, 502/503/504, and 429
// (honoring Retry-After) with backoff. Redirects and other statuses are
// returned as-is on the first response.
func (r *Remote) getStable(ctx context.Context, path string) (*http.Response, error) {
	resp, err := r.Get(ctx, path)
	for _, wait := range retryBackoff {
		if err == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		code := 0
		var serverWait time.Duration
		if resp != nil {
			code = resp.StatusCode
			serverWait = retryWait(resp, wait)
			_ = resp.Body.Close()
		}
		// Retries ride on the transport's keep-alive pool; if the pinned
		// connection's egress route is the thing that's broken, every retry
		// through it fails the same way. Drop the pool so the retry dials a
		// fresh connection (and, with a proxy, possibly a fresh exit).
		r.client.CloseIdleConnections()
		if ctx.Err() != nil {
			if code == 0 {
				return nil, err
			}
			return nil, &UpstreamStatusError{Path: path, Status: code}
		}
		select {
		case <-ctx.Done():
			if code == 0 {
				return nil, err
			}
			return nil, &UpstreamStatusError{Path: path, Status: code}
		case <-time.After(jitter(serverWait)):
		}
		resp, err = r.Get(ctx, path)
	}
	return resp, err
}

// TransientUpstream reports whether err is a transient upstream failure — a
// retryable status (429/5xx/transport) or a transport-level error — rather than
// a definitive answer (404) or a caller cancellation. Adapters use it to return
// 503 + Retry-After instead of a misleading 404 when an upstream fetch failed
// for a retryable reason.
func TransientUpstream(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var us *UpstreamStatusError
	if errors.As(err, &us) {
		return retryableStatus(us.Status)
	}
	// Any other error from the fetch paths is a transport-level failure.
	return true
}

// GetCached GETs through a TTL cache keyed by absolute URL (for small index
// documents re-fetched on every client resolution).
func (r *Remote) GetCached(ctx context.Context, cache *MemCache, path string) (string, error) {
	key := r.URL(path)
	if body, ok := cache.Get(key); ok {
		return body, nil
	}
	body, err := r.GetBytes(ctx, path)
	if err != nil {
		return "", err
	}
	s := string(body)
	cache.Set(key, s)
	return s, nil
}
