package hex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) pkg(w http.ResponseWriter, r *http.Request, name string) {
	versionList, _ := s.Registry.Meta.ListVersions(r.Context(), "hex", name)
	if len(versionList) == 0 {
		if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "hex"}); err == nil {
			if body, err := remote.GetBytes(r.Context(), "/packages/"+easyblobkit.URLencode(name)); err == nil {
				easyblobkit.OctetResponse(w, r, body)
				return
			}
		}
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	payload := []byte{}
	for _, v := range versionList {
		art, err := s.Registry.Meta.Get(r.Context(), "hex", name, v)
		if err != nil {
			continue
		}
		rel := pbStr(nil, 1, v)
		inner := [32]byte{}
		var m map[string]any
		if json.Unmarshal(art.Proprietary, &m) == nil {
			if ic, ok := m["inner_checksum"].(string); ok {
				if b, err := hex.DecodeString(ic); err == nil && len(b) == 32 {
					copy(inner[:], b)
				}
			}
		}
		// Ensure inner_checksum carries a real (non-zero) 32-byte value; an
		// all-zero value is not a valid checksum for the release. Fall back to
		// the sha256 of the stored .tar when no recorded value exists.
		if !bytesHasNonZero(inner[:]) && len(art.Blobs) > 0 {
			if h, err := s.Registry.BlobHashes(r.Context(), art.Blobs[0].Digest); err == nil {
				if raw, err := hex.DecodeString(h.SHA256); err == nil && len(raw) == 32 {
					copy(inner[:], raw)
				}
			}
		}
		rel = pbBytes(rel, 2, inner[:])
		// dependencies (field 3) is omitted when empty (matching hex.pm).
		// outer_checksum at field 5: emit the .tar sha256.
		if len(art.Blobs) > 0 {
			if h, err := s.Registry.BlobHashes(r.Context(), art.Blobs[0].Digest); err == nil {
				if raw, err := hex.DecodeString(h.SHA256); err == nil && len(raw) == 32 {
					rel = pbBytes(rel, 5, raw)
				}
			}
		}
		// retirement (message, field 7): emit a not-retired default; hex.pm
		// always ships field 7 with {retired_at, reason} varints.
		rel = pbBytes(rel, 7, pbRetirement())
		payload = pbBytes(payload, 1, rel)
	}
	payload = pbStr(payload, 2, name)
	payload = pbStr(payload, 3, "hexpm")
	s.signedGzip(w, r, payload)
}

func (s *State) tarball(w http.ResponseWriter, r *http.Request, rest string) {
	rest = strings.Trim(rest, "/")
	filename := rest[strings.LastIndex(rest, "/")+1:]
	stem := strings.TrimSuffix(filename, ".tar")
	name, version := nameVersionFromStem(stem)
	if art, err := s.Registry.Meta.Get(r.Context(), "hex", name, version); err == nil {
		for _, b := range art.Blobs {
			if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", filename) {
				return
			}
		}
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "hex"}); err == nil {
		if data, err := remote.GetBytes(r.Context(), "/tarballs/"+filename); err == nil {
			easyblobkit.ServeData(w, r, s.Registry, r.Context(), data, "application/octet-stream", filename)
			return
		} else {
			easyblobkit.WriteUpstreamError(w, err)
			return
		}
	}
	easyblobkit.Error(w, http.StatusNotFound, "not found")
}

func nameVersionFromStem(stem string) (string, string) {
	if i := strings.LastIndex(stem, "-"); i > 0 {
		return stem[:i], stem[i+1:]
	}
	return stem, "0.1.0"
}

func storeVersionSource(reg *easyblobkit.Registry, name, version, filename string, data []byte, source string, ctx context.Context) {
	// Persist the inner checksum (contents.tar.gz sha256) so the release
	// protobuf can emit a real inner_checksum rather than a placeholder.
	var prop []byte
	if inner := innerChecksumOf(data); inner != "" {
		prop = []byte(`{"inner_checksum":"` + inner + `"}`)
	}
	reg.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "hex", Repository: name, Version: version,
		Filename: filename, Source: source, Data: data, Proprietary: prop,
	})
}

// innerChecksumOf extracts the inner checksum a hex package publishes:
// sha256(VERSION || metadata.config || contents.tar.gz) over those files'
// raw bytes in the outer tar (matches mix_hex_tarball / the reference).
func innerChecksumOf(data []byte) string {
	var r io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return ""
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	tr := tar.NewReader(r)
	var version, metadata, contents []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		switch hdr.Name {
		case "VERSION":
			version, _ = io.ReadAll(tr)
		case "metadata.config":
			metadata, _ = io.ReadAll(tr)
		case "contents.tar.gz":
			contents, _ = io.ReadAll(tr)
		}
	}
	if version == nil || metadata == nil || contents == nil {
		return ""
	}
	var blob []byte
	blob = append(blob, version...)
	blob = append(blob, metadata...)
	blob = append(blob, contents...)
	h, _ := easyblobkit.ComputeHashesBytes(blob)
	return h.SHA256
}

func tarballNameVersion(data []byte) (string, string) {
	// Hex .tar is a plain (uncompressed) POSIX tar archive — not gzip. But
	// be tolerant of gzipped input too.
	var r io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return "", ""
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		if !strings.HasSuffix(hdr.Name, "metadata.config") && !strings.HasSuffix(hdr.Name, "VERSION") && !strings.HasSuffix(hdr.Name, "version") {
			continue
		}
		buf, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
		// metadata.config is Erlang term syntax: {<<"name">>,<<"...">>}.
		// VERSION is a plain file with the version string. Handle both.
		name := erlangTermField(string(buf), "name")
		version := erlangTermField(string(buf), "version")
		if name == "" && version == "" {
			// Try the earlier YAML-style parser as a fallback.
			name = yamlField(string(buf), "name")
			version = yamlField(string(buf), "version")
		}
		if name != "" || version != "" {
			return name, version
		}
	}
	return "", ""
}

// erlangTermField extracts the value of `{<<"key">>,<<"value">>}`-style or
// `{<<"key">>,Other}` terms (hex metadata.config is Erlang term format).
func erlangTermField(s, field string) string {
	// Look for {<<"field">>, ...}
	pfx := `{<<"` + field + `">>,`
	if i := strings.Index(s, pfx); i >= 0 {
		rest := s[i+len(pfx):]
		// After the comma, expect a <<"...">> string or another term; take the
		// first quoted string.
		if strings.HasPrefix(rest, `<<"`) {
			rest = rest[3:]
			if j := strings.Index(rest, `">>`); j >= 0 {
				return rest[:j]
			}
		}
		// bare atom or number form: {<<"name">>,some_atom}.
		end := strings.Index(rest, "}.")
		if end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	return ""
}

func yamlField(s, field string) string {
	prefix := field + ":"
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(t, prefix); ok {
			return strings.Trim(strings.TrimSpace(rest), `"'`)
		}
	}
	return ""
}
