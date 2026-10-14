package easyblobkit

import "net/http"

// Upstream failure → client response, shared by every adapter so a transient
// upstream blip is uniformly a retryable 503 rather than a hard 404.

// WriteUpstreamError maps an upstream fetch error onto a client response: a
// transient failure (transport error, or a retryable 429/5xx after the shared
// retry budget) becomes 503 + Retry-After (clients retry 5xx); anything else
// (a definitive 404/403) becomes 404. Adapters that fetch through Remote use it
// so a single egress blip does not abort a client build.
func WriteUpstreamError(w http.ResponseWriter, err error) {
	if TransientUpstream(err) {
		w.Header().Set("Retry-After", "2")
		Error(w, http.StatusServiceUnavailable, "upstream temporarily unavailable")
		return
	}
	Error(w, http.StatusNotFound, "not found")
}

// WriteCacheResultError maps a failed FetchCachedPath/FetchPath result onto a
// client response: Transient → 503 + Retry-After, else 404.
func WriteCacheResultError(w http.ResponseWriter, res PathCacheResult) {
	if res.Transient {
		w.Header().Set("Retry-After", "2")
		Error(w, http.StatusServiceUnavailable, "upstream temporarily unavailable")
		return
	}
	Error(w, http.StatusNotFound, "not found")
}

// WriteBlobResultError maps a failed FetchToBlobResult onto a client response:
// Transient → 503 + Retry-After, else 404.
func WriteBlobResultError(w http.ResponseWriter, res BlobFetchResult) {
	if res.Transient {
		w.Header().Set("Retry-After", "2")
		Error(w, http.StatusServiceUnavailable, "upstream temporarily unavailable")
		return
	}
	Error(w, http.StatusNotFound, "not found")
}
