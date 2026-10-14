package pypi

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) metadataFile(w http.ResponseWriter, r *http.Request, project, filename string) {
	// A cached wheel's real METADATA is authoritative (pip verifies it against
	// the index's data-dist-info-metadata hash).
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "pypi", project)
	for _, v := range versions {
		art, err := s.Registry.Meta.Get(r.Context(), "pypi", project, v)
		if err != nil {
			continue
		}
		for _, b := range art.Blobs {
			if b.Name != filename {
				continue
			}
			rd, err := s.Registry.Blobs.Open(r.Context(), b.Digest)
			if err != nil || rd == nil {
				continue
			}
			data, _ := io.ReadAll(rd)
			_ = rd.Close()
			if meta := extractWheelMetadata(data); meta != "" {
				easyblobkit.Text(w, http.StatusOK, meta, "application/octet-stream")
				return
			}
			// Cached sdist / unparsable wheel: fall through to upstream.
			break
		}
	}
	// Pull-through (PEP 658): resolve the wheel/sdist URL from the upstream
	// /simple/ page and fetch "<url>.metadata". This preserves the upstream
	// hash pip expects; only synthesize when there is no upstream at all.
	base := s.Registry.Upstreams.Get("pypi")
	if base != "" {
		pagePath := "/simple/" + easyblobkit.URLencode(project) + "/"
		remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Base: base})
		page, err := remote.GetBytes(r.Context(), pagePath)
		if err == nil {
			pageURL := base + pagePath
			fileURL := resolveFileHref(string(page), filename, pageURL)
			if fileURL != "" {
				if meta, err := remote.GetBytes(r.Context(), strings.TrimPrefix(fileURL+".metadata", base)); err == nil {
					easyblobkit.Text(w, http.StatusOK, string(meta), "application/octet-stream")
					return
				}
				if m2, err := s.Registry.FetchAbsolute(r.Context(), fileURL+".metadata"); err == nil {
					easyblobkit.Text(w, http.StatusOK, string(m2.Data), "application/octet-stream")
					return
				}
			}
		}
	}
	version := versionFromFilename(filename, project)
	easyblobkit.Text(w, http.StatusOK,
		"Metadata-Version: 2.1\nName: "+project+"\nVersion: "+version+"\n", "application/octet-stream")
}

func extractWheelMetadata(data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".dist-info/METADATA") {
			rc, err := f.Open()
			if err != nil {
				return ""
			}
			defer func() { _ = rc.Close() }()
			b, err := io.ReadAll(rc)
			if err != nil {
				return ""
			}
			return string(b)
		}
	}
	return ""
}
