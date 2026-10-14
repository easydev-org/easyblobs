package git

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// lfsBatch proxies the LFS batch negotiation upstream and rewrites every
// action href to the mirror, so object transfers come back to lfsObject.
func (s *State) lfsBatch(w http.ResponseWriter, r *http.Request, host, repoPath string) {
	easyblobkit.LimitBody(w, r)
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
	base := "https://" + host
	remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: base})
	resp, err := remote.Do(r.Context(), http.MethodPost,
		"/"+strings.Trim(repoPath, "/")+"/info/lfs/objects/batch",
		bytes.NewReader(body), r.Header.Get("Content-Type"), r.Header.Get("Accept"))
	if err != nil {
		easyblobkit.Error(w, http.StatusBadGateway, "lfs upstream: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusOK {
		out = s.rewriteLFSBatch(r, host, repoPath, out)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// lfsObject serves a download from the CAS, fetching it from the upstream on a
// miss. The upstream object URL is a presigned, expiring CDN href that the
// batch response carried, so on a miss we re-run the batch for this one oid and
// fetch the href it returns. Uploads (PUT) store the object locally.
func (s *State) lfsObject(w http.ResponseWriter, r *http.Request, host, repoPath, verb, oid string) {
	if !validLFSObjectPath(verb, oid) {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	digest := "sha256:" + oid
	switch {
	case r.Method == http.MethodGet && verb == "download":
		if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), digest, "application/octet-stream") {
			return
		}
		href, err := s.upstreamLFSHref(r.Context(), host, repoPath, oid, lfsSize(r))
		if err != nil {
			easyblobkit.Error(w, http.StatusBadGateway, "lfs upstream: "+err.Error())
			return
		}
		if _, err := s.Registry.FetchAbsoluteToBlob(r.Context(), href, digest); err != nil {
			easyblobkit.Error(w, http.StatusBadGateway, "lfs fetch: "+err.Error())
			return
		}
		if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), digest, "application/octet-stream") {
			return
		}
		easyblobkit.Error(w, http.StatusBadGateway, "lfs cache error")
	case r.Method == http.MethodPut && verb == "upload":
		if !easyblobkit.AuthorizeWriteFor(w, r, s.Auth, s.Registry, "git", repoPath) {
			return
		}
		if _, _, err := s.Registry.Blobs.Put(r.Context(), r.Body, digest); err != nil {
			easyblobkit.Error(w, http.StatusBadRequest, "lfs digest mismatch: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		easyblobkit.Error(w, http.StatusNotFound, "not found")
	}
}

// upstreamLFSHref asks the upstream LFS batch for one object's download href.
func (s *State) upstreamLFSHref(ctx context.Context, host, repoPath, oid string, size int64) (string, error) {
	base := "https://" + host
	remote, _ := s.Registry.Remote(ctx, easyblobkit.UpstreamSpec{Base: base})
	reqBody := `{"operation":"download","transfers":["basic"],"objects":[{"oid":"` + oid + `","size":` + strconv.FormatInt(size, 10) + `}]}`
	resp, err := remote.Do(ctx, http.MethodPost,
		"/"+strings.Trim(repoPath, "/")+"/info/lfs/objects/batch",
		strings.NewReader(reqBody), "application/vnd.git-lfs+json", "application/vnd.git-lfs+json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var doc struct {
		Objects []struct {
			Actions map[string]struct {
				Href string `json:"href"`
			} `json:"actions"`
		} `json:"objects"`
	}
	if json.Unmarshal(body, &doc) != nil || len(doc.Objects) == 0 {
		return "", errUnknownLFSObject
	}
	if a, ok := doc.Objects[0].Actions["download"]; ok && a.Href != "" {
		return a.Href, nil
	}
	return "", errUnknownLFSObject
}

var errUnknownLFSObject = errUnknownLFSObjectType("lfs object not found")

type errUnknownLFSObjectType string

func (e errUnknownLFSObjectType) Error() string { return string(e) }

// validLFSObjectPath reports whether verb/oid name a real object transfer.
func validLFSObjectPath(verb, oid string) bool {
	if verb != "download" && verb != "upload" {
		return false
	}
	if len(oid) != 64 {
		return false
	}
	for _, c := range oid {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// rewriteLFSBatch points every batch action at the mirror's own LFS endpoint,
// so the object fetch is served (and cached) by us. The upstream's
// "href"/"header" fields are replaced; the batch response shape is otherwise
// preserved.
func (s *State) rewriteLFSBatch(r *http.Request, host, repoPath string, body []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return body
	}
	objs, _ := doc["objects"].([]any)
	// Build the prefix the client should use to reach this object. Two shapes
	// exist: with a raw self-base the adapter emits the upstream origin
	// (https://github.com/<repo>/...) and the egress proxy maps it back; with a
	// mount self-base it emits the gateway path, which carries the host segment
	// (/artifacts/git/<host>/<repo>/...).
	repo := strings.Trim(repoPath, "/")
	var prefix string
	switch base := strings.TrimSuffix(s.SelfBase, "/"); {
	case base == "":
		prefix = s.selfBase(r) + "/artifacts/git/" + host + "/" + repo
	case strings.Contains(base, "/artifacts/"):
		prefix = base + "/" + host + "/" + repo
	default:
		prefix = base + "/" + repo
	}
	for _, o := range objs {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		actions, ok := m["actions"].(map[string]any)
		if !ok {
			continue
		}
		size := sizeOf(m)
		for _, verb := range []string{"download", "upload", "verify"} {
			a, ok := actions[verb].(map[string]any)
			if !ok {
				continue
			}
			// verify actions keep their upstream href (they validate a local
			// object; there is no object to fetch).
			if verb != "verify" {
				// Carry the object size in the href: the upstream batch API
				// requires it, and the object fetch is a separate request.
				a["href"] = prefix + "/info/lfs/objects/" + verb + "/" + oidOf(m) + "?size=" + strconv.FormatInt(size, 10)
				delete(a, "header")
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}

func oidOf(m map[string]any) string {
	if oid, ok := m["oid"].(string); ok {
		return oid
	}
	return ""
}

func sizeOf(m map[string]any) int64 {
	if n, ok := m["size"].(float64); ok {
		return int64(n)
	}
	return 0
}

// lfsSize reads the ?size= the batch href carried.
func lfsSize(r *http.Request) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64)
	return n
}
