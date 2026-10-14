package easyblobkit

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// Registry implementation ----------------------------------------------------

// Fetch pulls `path` from an upstream and caches it in the blob store. A
// redirect from the upstream (3xx with Location) is surfaced as an error so
// callers can decide how to handle it instead of silently downloading the
// whole target server-side.
func (r *Registry) Fetch(ctx context.Context, format, upstreamBase, path string) (Fetched, error) {
	if upstreamBase == "" {
		// No explicit base: resolve by the full priority and restore the
		// client's stripped prefix (maven's /maven2, helm's /stable) so the
		// upstream sees the path it published.
		return r.FetchPath(ctx, format, path)
	}
	return r.fetchWith(ctx, r.remoteAtBase(upstreamBase), path)
}

// FetchPath pulls a format-relative path using the request's full upstream
// priority (per-repo override → client origin → table default), prepending the
// client's stripped prefix so the upstream sees the original path. Adapters
// call this instead of assembling a base themselves.
func (r *Registry) FetchPath(ctx context.Context, format, path string) (Fetched, error) {
	sc := RepoScopeFrom(ctx)
	repo := sc.Namespace
	if repo == "" {
		repo = sc.Name
	}
	remote, err := r.remoteFor(ctx, format, repo, "")
	if err != nil {
		return Fetched{}, err
	}
	return r.fetchWith(ctx, remote, path)
}

// fetchWith issues one GET against a resolved upstream and caches the bytes.
// Concurrent identical fetches (same URL and auth headers) are collapsed into
// one upstream request by a process-wide single-flight group.
func (r *Registry) fetchWith(ctx context.Context, remote *Remote, path string) (Fetched, error) {
	v, err, _ := fetchGroup.Do(flightKey(flightFetched, remote, path), func() (any, error) {
		return r.fetchWithDirect(ctx, remote, path)
	})
	if err != nil {
		return Fetched{}, err
	}
	return v.(Fetched), nil
}

// fetchWithDirect is fetchWith without the single-flight wrapper.
func (r *Registry) fetchWithDirect(ctx context.Context, remote *Remote, path string) (Fetched, error) {
	resp, err := remote.getStable(ctx, path)
	if err != nil {
		return Fetched{}, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return Fetched{}, &UpstreamRedirectError{Path: path, Status: resp.StatusCode, Location: resp.Header.Get("Location")}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Fetched{}, &UpstreamStatusError{Path: path, Status: resp.StatusCode}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Fetched{}, err
	}
	return r.finishFetch(ctx, data)
}

// UpstreamRedirectError reports a 3xx from an upstream.
type UpstreamRedirectError struct {
	Path     string
	Status   int
	Location string
}

func (e *UpstreamRedirectError) Error() string {
	return fmt.Sprintf("upstream %s: redirected (%d) to %s", e.Path, e.Status, e.Location)
}

// FetchAbsolute pulls a full URL verbatim.
func (r *Registry) FetchAbsolute(ctx context.Context, url string) (Fetched, error) {
	factory := sharedFactory
	remote := NewRemote(factory, "", proxyPtr(r.Upstreams, "generic"))
	resp, err := remote.getStable(ctx, url)
	if err != nil {
		return Fetched{}, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Fetched{}, &UpstreamStatusError{Path: url, Status: resp.StatusCode}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Fetched{}, err
	}
	return r.finishFetch(ctx, data)
}

// FetchAbsoluteBytes pulls a full URL verbatim and returns its bytes.
func (r *Registry) FetchAbsoluteBytes(ctx context.Context, url string) ([]byte, error) {
	fetched, err := r.FetchAbsolute(ctx, url)
	if err != nil {
		return nil, err
	}
	return fetched.Data, nil
}

// GetAbsoluteRange issues a ranged GET against an absolute URL with the
// registry's proxy policy and following redirects, returning the upstream
// response (the caller relays it). It lets an adapter pass a client's Range
// through to a CDN that supports it, instead of caching the whole object first.
func (r *Registry) GetAbsoluteRange(ctx context.Context, url, rangeHeader string) (*http.Response, error) {
	factory := sharedFactory
	remote := NewRemote(factory, "", proxyPtr(r.Upstreams, "generic"))
	client := &http.Client{Transport: remote.client.Transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	return client.Do(req)
}

// FetchAbsoluteToBlob streams an absolute URL into the CAS, verifying it
// matches wantDigest (when non-empty) and following redirects. It is used for
// presigned CDN hrefs (git LFS objects) that the caller learned out of band.
func (r *Registry) FetchAbsoluteToBlob(ctx context.Context, url, wantDigest string) (int64, error) {
	_, size, err := r.FetchAbsoluteToBlobDigest(ctx, url, wantDigest)
	return size, err
}

// FetchAbsoluteToBlobDigest is FetchAbsoluteToBlob but also returns the stored
// blob's digest, so a caller can index it (e.g. cache an LFS file by its
// identity while the bytes live in the content-addressed CAS).
func (r *Registry) FetchAbsoluteToBlobDigest(ctx context.Context, url, wantDigest string) (string, int64, error) {
	return r.fetchAbsolute(ctx, url, wantDigest, nil)
}

// StreamToBlobAndClient fetches an absolute URL and, in one pass, streams the
// bytes BOTH into the CAS and to the client (a tee), so the client's first byte
// arrives immediately instead of after the whole file is buffered. It returns
// the stored digest/size so the caller can index the file by identity. The
// caller must have already written the response headers + status (200): the
// bytes start flowing to the client as soon as they leave the upstream.
//
// On a mid-stream error the client has already received a (truncated) 200, so
// the caller must NOT index a partial blob; the error is returned for logging.
func (r *Registry) StreamToBlobAndClient(ctx context.Context, url string, w io.Writer) (string, int64, error) {
	return r.fetchAbsolute(ctx, url, "", w)
}

// fetchAbsolute is the shared GET-and-store path. When tee is non-nil the bytes
// are copied to it while being stored.
func (r *Registry) fetchAbsolute(ctx context.Context, url, wantDigest string, tee io.Writer) (string, int64, error) {
	factory := sharedFactory
	remote := NewRemote(factory, "", proxyPtr(r.Upstreams, "generic"))
	// Resumable GET that follows redirects: a presigned CDN href may 3xx, and a
	// multi-GB CDN transfer can drop mid-stream (a reported ~7.6 GB cut). The
	// resumable body retries the drop with a Range request from the last byte, so
	// the whole object still lands in the CAS and a teed client is not truncated.
	// A fresh client over the same transport follows redirects (the shared
	// client's CheckRedirect stops at the first hop).
	followClient := &http.Client{Transport: remote.client.Transport}
	body, err := openResumable(ctx, followClient, url, remote.headers)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = body.Close() }()
	// Track this fetch (bytes + total) for GET /artifacts/system/downloads.
	dl := activeDownloads.begin(url, "", tee != nil, body.ContentLength())
	defer activeDownloads.end(dl)
	var src io.Reader = &countingReader{r: body, e: dl}
	if tee != nil {
		src = io.TeeReader(src, tee)
	}
	// The store verifies wantDigest when set and computes it when empty, so an
	// empty digest is safe (the bytes are stored under their true identity).
	stored, _, err := r.Blobs.Put(ctx, src, wantDigest)
	if err != nil {
		return "", 0, err
	}
	return stored.Digest, stored.Size, nil
}

func (r *Registry) finishFetch(ctx context.Context, data []byte) (Fetched, error) {
	stored, err := r.StoreAndHash(ctx, data)
	if err != nil {
		return Fetched{}, err
	}
	return Fetched{Data: data, Hashes: stored.Hashes, Size: stored.Size, Digest: stored.Digest}, nil
}
