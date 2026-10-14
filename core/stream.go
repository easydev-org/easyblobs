package easyblobkit

import (
	"context"
	"io"
	"net/http"
	"time"
)

// GetBytesFollow is GetBytes but follows redirects (up to the default policy).
// Ivy servers redirect to their artifact store (repo.scala-sbt.org →
// scala.jfrog.io) and the redirected body is what must be cached. Concurrent
// identical requests are collapsed by the same single-flight group.
func (r *Remote) GetBytesFollow(ctx context.Context, path string) ([]byte, error) {
	v, err, _ := fetchGroup.Do(flightKey(flightDoc, r, path), func() (any, error) {
		return r.getBytesFollowDirect(ctx, path)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// getBytesFollowDirect is GetBytesFollow without the single-flight wrapper.
func (r *Remote) getBytesFollowDirect(ctx context.Context, path string) ([]byte, error) {
	// Use the SHARED retrying GET (transport errors + 502/503/504 + 429 with
	// backoff), which also follows redirects — the module .zip is redirected to
	// a presigned CDN URL on a cold edge cache. A bare client.Do here gave the
	// .zip path NO retry, so a single egress-proxy 502 failed the whole module.
	resp, err := r.getStreamRetry(ctx, path)
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

// FetchPathFollow is FetchPath but follows redirects, caching the final body.
// It is for the plain-HTTP trees whose roots redirect to a regional mirror
// (pkg.julialang.org → in.pkg.julialang.org) and for registries that redirect
// to an artifact store the mirror should read through (Ivy).
func (r *Registry) FetchPathFollow(ctx context.Context, format, path string) (Fetched, error) {
	sc := RepoScopeFrom(ctx)
	repo := sc.Namespace
	if repo == "" {
		repo = sc.Name
	}
	remote, err := r.remoteFor(ctx, format, repo, "")
	if err != nil {
		return Fetched{}, err
	}
	v, err, _ := fetchGroup.Do(flightKey(flightFetched, remote, path), func() (any, error) {
		data, err := remote.GetBytesFollow(ctx, path)
		if err != nil {
			return Fetched{}, err
		}
		return r.finishFetch(ctx, data)
	})
	if err != nil {
		return Fetched{}, err
	}
	return v.(Fetched), nil
}

// FetchToBlob streams a remote object into the CAS and returns its digest and
// size. Unlike Fetch it never buffers the whole body in memory, so it is the
// right call for the large, range-addressable files of the plain-HTTP trees
// (Hackage's 100MB+ index) and for anything a client may request a Range of.
// Redirects are followed (a tree root may point at a regional mirror).
func (r *Registry) FetchToBlob(ctx context.Context, format, path string) (string, int64, bool) {
	res := r.FetchToBlobResult(ctx, format, path)
	return res.Digest, res.Size, res.OK
}

// BlobFetchResult is the outcome of FetchToBlobResult.
type BlobFetchResult struct {
	Digest string
	Size   int64
	OK     bool
	// Transient is true when !OK because the upstream FAILED transiently rather
	// than the object definitively not existing (map to 503 + Retry-After).
	Transient bool
}

// FetchToBlobResult is FetchToBlob with the transient/definitive distinction.
func (r *Registry) FetchToBlobResult(ctx context.Context, format, path string) BlobFetchResult {
	sc := RepoScopeFrom(ctx)
	repo := sc.Namespace
	if repo == "" {
		repo = sc.Name
	}
	remote, err := r.remoteFor(ctx, format, repo, "")
	if err != nil {
		return BlobFetchResult{}
	}
	type blobResult struct {
		digest    string
		size      int64
		ok        bool
		transient bool
	}
	v, _, _ := fetchGroup.Do(flightKey(flightBlob, remote, path), func() (any, error) {
		digest, size, ok, transient := r.fetchToBlobDirect(ctx, remote, path)
		return blobResult{digest, size, ok, transient}, nil
	})
	res := v.(blobResult)
	return BlobFetchResult{Digest: res.digest, Size: res.size, OK: res.ok, Transient: res.transient}
}

// getStreamRetry issues a GET and returns the response, retrying transport
// errors and 502/503/504 with backoff — the same resilience getStable gives
// the buffered paths. It is required for the streaming fetch paths: the egress
// proxy surfaces upstream blips as 502, and a one-shot GET would fail a client
// build. The returned response is a fresh body each attempt; callers must close
// it. Redirects are followed (the streaming trees may redirect to a mirror).
func (r *Remote) getStreamRetry(ctx context.Context, path string) (*http.Response, error) {
	return r.getStreamRetryCond(ctx, path, nil)
}

// getStreamRetryCond is getStreamRetry with optional extra request headers
// (If-None-Match / If-Modified-Since for a revalidation). A 304 is a valid
// result and is returned immediately (never treated as retryable).
func (r *Remote) getStreamRetryCond(ctx context.Context, path string, extra map[string]string) (*http.Response, error) {
	client := &http.Client{Transport: r.client.Transport}
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL(path), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		for k, v := range r.headers {
			req.Header.Set(k, v)
		}
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		return req, nil
	}
	do := func() (*http.Response, error) {
		req, err := newReq()
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	resp, err := do()
	for _, wait := range retryBackoff {
		if err == nil && !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		code := 0
		serverWait := wait
		if resp != nil {
			code = resp.StatusCode
			serverWait = retryWait(resp, wait)
			_ = resp.Body.Close()
		}
		r.client.CloseIdleConnections()
		select {
		case <-ctx.Done():
			if code == 0 {
				return nil, err
			}
			return nil, &UpstreamStatusError{Path: path, Status: code}
		case <-time.After(serverWait):
		}
		resp, err = do()
	}
	return resp, err
}

// fetchToBlobDirect is FetchToBlob without the single-flight wrapper.
func (r *Registry) fetchToBlobDirect(ctx context.Context, remote *Remote, path string) (string, int64, bool, bool) {
	resp, err := remote.getStreamRetry(ctx, path)
	if err != nil {
		return "", 0, false, TransientUpstream(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", 0, false, retryableStatus(resp.StatusCode)
	}
	// The store streams the body into the CAS and hashes it in one pass.
	dl := activeDownloads.begin(path, "", false, resp.ContentLength)
	defer activeDownloads.end(dl)
	stored, _, err := r.Blobs.Put(ctx, &countingReader{r: resp.Body, e: dl}, "")
	if err != nil {
		return "", 0, false, true
	}
	return stored.Digest, stored.Size, true, false
}
