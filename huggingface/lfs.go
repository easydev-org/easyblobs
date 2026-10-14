package huggingface

import (
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/easydev-org/easyblobs/core"
)

// cacheLFS reports whether LFS weights are fetched into the CAS server-side.
// EASYBLOBS_HF_DISABLE_LFS_CACHE=1 (or cfg disable_lfs_cache) restores the old
// pass-the-redirect-through behaviour.
func (s *State) cacheLFS() bool {
	if s.DisableLFSCache {
		return false
	}
	return os.Getenv("EASYBLOBS_HF_DISABLE_LFS_CACHE") != "1"
}

// serveLFS fetches an LFS file's bytes from its CDN URL into the CAS and serves
// them locally, caching by the file's identity (repo/rev/path) so a repeat pull
// hits local storage. The Hub headers (X-Repo-Commit et al) are recorded and
// replayed so huggingface_hub accepts the response as a Hub endpoint.
func (s *State) serveLFS(w http.ResponseWriter, r *http.Request, cdnURL, repoKey, path string, resp *http.Response) {
	// Defensive: a HEAD must never trigger the (multi-GB) download.
	if r.Method == http.MethodHead {
		forwardHubHeaders(w, resp)
		if v := resp.Header.Get("X-Linked-Size"); v != "" {
			w.Header().Set("Content-Length", v)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	// A Range request (a resume) must be answered PROMPTLY: proxying it to the
	// CDN (which supports Range) returns 206 immediately, whereas "cache the
	// whole 10 GB first, then serve" makes the client read-time out and restart
	// from zero — the reported death loop. The common no-Range GET streams (and
	// fills the CAS).
	if r.Header.Get("Range") != "" {
		s.serveLFSRange(w, r, cdnURL, resp)
		return
	}
	s.serveLFSStreaming(w, r, cdnURL, repoKey, path, resp)
}

// serveLFSRange proxies a Range request to the CDN (which honours Range) so a
// resume is served immediately as 206 + Content-Range, without waiting for the
// whole file to be cached. A Range request does not fill the CAS (it is a
// partial transfer); a later full GET caches the object.
func (s *State) serveLFSRange(w http.ResponseWriter, r *http.Request, cdnURL string, resp *http.Response) {
	out, err := s.Registry.GetAbsoluteRange(r.Context(), cdnURL, r.Header.Get("Range"))
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "huggingface lfs range: "+err.Error())
		return
	}
	defer func() { _ = out.Body.Close() }()
	// Relay the range response verbatim (status 206 + Content-Range/Length/Accept-Ranges).
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := out.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	forwardHubHeaders(w, resp)
	w.WriteHeader(out.StatusCode)
	_, _ = io.Copy(w, out.Body)
}

// serveLFSStreaming streams an LFS file to the client WHILE storing it in the
// CAS (a tee), so the first byte arrives immediately instead of after the whole
// multi-GB file is buffered. The index row is written only after a complete
// read, so a truncated transfer is never cached as complete.
func (s *State) serveLFSStreaming(w http.ResponseWriter, r *http.Request, cdnURL, repoKey, path string, resp *http.Response) {
	ct := contentType(resp.Header.Get("Content-Type"))
	if ct == "" || ct == "application/octet-stream" {
		ct = "application/octet-stream"
	}
	// Write the headers + 200 up front; the body streams as the upstream sends.
	forwardHubHeaders(w, resp)
	if v := resp.Header.Get("X-Linked-Size"); v != "" {
		w.Header().Set("Content-Length", v)
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	digest, size, err := s.Registry.StreamToBlobAndClient(r.Context(), cdnURL, w)
	if err != nil {
		// The client already got a (truncated) 200; do not index a partial blob.
		easyblobkit.LogMetaErr("huggingface lfs stream", err)
		return
	}
	hdrs := map[string]string{}
	for _, h := range hubHeaderAllow {
		if v := resp.Header.Get(h); v != "" {
			hdrs[h] = v
		}
	}
	prop, _ := json.Marshal(map[string]any{"headers": hdrs})
	easyblobkit.LogMetaErr("huggingface lfs cache", s.Registry.Meta.Put(r.Context(), easyblobkit.Artifact{
		Format: "huggingface", Repository: repoKey, Version: path,
		MediaType: ct, Digest: digest, Proprietary: prop,
		Blobs:  []easyblobkit.Descriptor{{Digest: digest, Size: size, Name: lastSeg(path)}},
		Source: "pull",
	}))
}
