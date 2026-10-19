package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) tarball(w http.ResponseWriter, r *http.Request, name, file string) {
	if !easyblobkit.AuthorizeReadFor(w, r, s.Auth, s.Registry, "npm", name) {
		return
	}
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "npm", name)
	for _, v := range versions {
		if tarballFilename(name, v) == file {
			if art, err := s.Registry.Meta.Get(r.Context(), "npm", name, v); err == nil {
				for _, b := range art.Blobs {
					if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", file) {
						return
					}
				}
			}
		}
	}
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "npm"})
	if err != nil {
		easyblobkit.Error(w, http.StatusNotFound, "tarball not found")
		return
	}
	data, pub, err := s.fetchTarball(r.Context(), remote, name, file)
	if err != nil {
		s.tarballErr(w, err)
		return
	}
	ver, pkgJSON := parseTarballPackageJSON(data)
	if ver == "" {
		ver = versionFromTarballName(file, name)
	}
	s.storeVersionAt(r.Context(), name, ver, data, pkgJSON, "pull", pub)
	easyblobkit.ServeData(w, r, s.Registry, r.Context(), data, "application/octet-stream", file)
}

// tarballFetchAttempts bounds the retry of a tarball 404. npm tarballs are
// IMMUTABLE, so a 404 is almost always an upstream blip (the observed "random
// 404 on a tarball that exists"), and retrying it is cheap: a genuinely-missing
// tarball simply 404s again. Transient failures (502/503/504/429/transport) are
// NOT retried here — core's getStable already retries them with backoff; a
// still-failing one is mapped to 503 below.
const tarballFetchAttempts = 3

// fetchTarball fetches a tarball, retrying a 404 a bounded number of times. It
// tries the conventional "/<name>/-/<file>" path first, then the packument's
// dist.tarball (JSR's npm-compatibility layer serves the tarball at a
// registry-specific URL).
func (s *State) fetchTarball(ctx context.Context, remote *easyblobkit.Remote, name, file string) ([]byte, time.Time, error) {
	var lastErr error
	for attempt := 0; attempt < tarballFetchAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		data, err := remote.GetBytes(ctx, "/"+encodeName(name)+"/-/"+file)
		if err == nil {
			return data, time.Time{}, nil
		}
		// A transient failure is already core-exhausted; do not also query the
		// packument (it would fail the same way) — surface it as 503 below.
		if !isTarballNotFound(err) {
			return nil, time.Time{}, err
		}
		// Conventional path 404'd: a JSR-style registry serves the tarball at a
		// registry-specific URL in the packument's dist.tarball. Try it.
		data, pub, perr := s.tarballFromPackument(ctx, remote, name, file)
		if perr == nil {
			return data, pub, nil
		}
		lastErr = perr
		// Definitive: the version's tarball is not in the packument at all.
		if errors.Is(perr, errNoTarball) {
			return nil, time.Time{}, perr
		}
		// A transient failure on the packument fetch is likewise surfaced as 503.
		if !isTarballNotFound(perr) {
			return nil, time.Time{}, perr
		}
		// Both paths 404'd: retry (tarballs are immutable — a blip clears).
	}
	return nil, time.Time{}, lastErr
}

// isTarballNotFound reports whether a failed tarball fetch is a definitive 404
// (worth retrying only because tarballs are immutable). A transient error, or a
// "not in the packument" sentinel, is not.
func isTarballNotFound(err error) bool {
	if err == nil || errors.Is(err, errNoTarball) {
		return false
	}
	var us *easyblobkit.UpstreamStatusError
	return errors.As(err, &us) && us.Status == http.StatusNotFound
}

// tarballErr maps a failed tarball fetch onto a client response. A transient
// upstream failure becomes 503 + Retry-After (npm honors it and retries 5xx),
// so a blip no longer aborts `npm ci` with a hard 404; a definitive absence
// stays 404.
func (s *State) tarballErr(w http.ResponseWriter, err error) {
	if !errors.Is(err, errNoTarball) && easyblobkit.TransientUpstream(err) {
		w.Header().Set("Retry-After", "2")
		easyblobkit.Error(w, http.StatusServiceUnavailable, "upstream temporarily unavailable")
		return
	}
	easyblobkit.Error(w, http.StatusNotFound, "tarball not found")
}

// tarballFromPackument resolves a tarball via the packument's dist.tarball
// URLs. Some registries (JSR's npm-compatibility layer) do not serve the
// conventional "/<name>/-/<file>" path: the tarball lives at a
// registry-specific URL carried in the packument. We derive the version from
// the requested filename and fetch that version's dist.tarball.
func (s *State) tarballFromPackument(ctx context.Context, remote *easyblobkit.Remote, name, file string) ([]byte, time.Time, error) {
	ver := versionFromTarballName(file, name)
	if ver == "" {
		return nil, time.Time{}, errNoTarball
	}
	body, err := remote.GetBytes(ctx, "/"+encodeName(name))
	if err != nil {
		return nil, time.Time{}, err
	}
	var doc struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
		// The packument's `time` map: {"created":…, "modified":…, "<version>":…}.
		Time map[string]string `json:"time"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return nil, time.Time{}, errNoTarball
	}
	v, ok := doc.Versions[ver]
	if !ok || v.Dist.Tarball == "" {
		return nil, time.Time{}, errNoTarball
	}
	var pub time.Time
	if ts := doc.Time[ver]; ts != "" {
		if t, perr := time.Parse(time.RFC3339, ts); perr == nil {
			pub = t
		}
	}
	fetched, err := s.Registry.FetchAbsolute(ctx, v.Dist.Tarball)
	if err != nil {
		return nil, time.Time{}, err
	}
	// The packument's per-version `time` wins; the tarball's Last-Modified is
	// the fallback.
	if pub.IsZero() {
		pub = fetched.PublishedAt
	}
	return fetched.Data, pub, nil
}

// errNoTarballType is a sentinel error for the packument fallback.
type errNoTarballType string

func (e errNoTarballType) Error() string { return string(e) }

var errNoTarball = errNoTarballType("tarball not found")

func versionFromTarballName(file, name string) string {
	short := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		short = name[i+1:]
	}
	prefix := short + "-"
	if strings.HasPrefix(file, prefix) {
		return strings.TrimSuffix(strings.TrimPrefix(file, prefix), ".tgz")
	}
	return ""
}

func parseTarballPackageJSON(data []byte) (string, map[string]any) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", nil
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if strings.HasSuffix(hdr.Name, "package.json") {
			buf, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			var m map[string]any
			if json.Unmarshal(buf, &m) == nil {
				ver, _ := m["version"].(string)
				return ver, m
			}
		}
	}
	return "", nil
}
