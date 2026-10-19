package pypi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) simpleFile(w http.ResponseWriter, r *http.Request, project, filename string) {
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "pypi", project)
	for _, v := range versions {
		art, err := s.Registry.Meta.Get(r.Context(), "pypi", project, v)
		if err != nil {
			continue
		}
		for _, b := range art.Blobs {
			if b.Name == filename {
				if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", filename) {
					return
				}
			}
		}
	}
	// Pull-through: resolve from the upstream /simple/ page.
	base := s.Registry.Upstreams.Get("pypi")
	if base == "" {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	pagePath := "/simple/" + easyblobkit.URLencode(project) + "/"
	remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: base})
	html, err := remote.GetBytes(r.Context(), pagePath)
	if err != nil {
		easyblobkit.WriteUpstreamError(w, err)
		return
	}
	pageURL := base + pagePath
	href := resolveFileHref(string(html), filename, pageURL)
	if href == "" {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	fetched, err := s.Registry.FetchAbsolute(r.Context(), href)
	if err != nil {
		easyblobkit.WriteUpstreamError(w, err)
		return
	}
	version := versionFromFilename(filename, project)
	s.storeVersionAt(project, version, filename, fetched.Data, "pull", fetched.PublishedAt, r.Context())
	easyblobkit.ServeData(w, r, s.Registry, r.Context(), fetched.Data, "application/octet-stream", filename)
}

func resolveFileHref(html, filename, baseURL string) string {
	rest := html
	for {
		start := strings.Index(rest, "<a ")
		if start < 0 {
			return ""
		}
		after := rest[start:]
		gt := strings.Index(after, ">")
		close := strings.Index(after, "</a>")
		if gt < 0 || close < 0 {
			return ""
		}
		fname := strings.TrimSpace(after[gt+1 : close])
		if i := strings.Index(fname, "#"); i >= 0 {
			fname = fname[:i]
		}
		if fname == filename {
			tag := after[:gt]
			if i := strings.Index(tag, `href="`); i >= 0 {
				t := tag[i+len(`href="`):]
				if j := strings.Index(t, `"`); j >= 0 {
					return dropFragment(resolveAgainst(t[:j], baseURL))
				}
			}
		}
		rest = after[close+len("</a>"):]
	}
}

func resolveAgainst(raw, baseURL string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.String()
	}
	if b, err := url.Parse(baseURL); err == nil {
		if j, err := b.Parse(raw); err == nil {
			return j.String()
		}
	}
	return raw
}

func dropFragment(s string) string {
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i]
	}
	return s
}

func versionFromFilename(filename, project string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.gz"), ".whl"), ".zip")
	rest := strings.TrimPrefix(base, project+"-")
	// Wheel filenames are {dist}-{version}(-{build})?-{py}-{abi}-{plat}; the
	// distribution never contains a hyphen, so the version is the first
	// remaining segment. sdists are {name}-{version}.
	if strings.HasSuffix(filename, ".whl") {
		if i := strings.Index(rest, "-"); i >= 0 {
			return rest[:i]
		}
	}
	return rest
}
