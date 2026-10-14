package rubygems

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) quickMarshal(w http.ResponseWriter, r *http.Request, rel string) {
	rel = strings.TrimPrefix(rel, "Marshal.4.8/")
	stem := strings.TrimSuffix(rel, ".gemspec.rz")
	name, version := nameVersionFromStem(stem)
	if _, err := s.Registry.Meta.Get(r.Context(), "rubygems", name, version); err == nil {
		spec := marshalSpecification(name, version)
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		_, _ = zw.Write(spec)
		_ = zw.Close()
		easyblobkit.OctetResponse(w, r, z.Bytes())
		return
	}
	remote, _ := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems"})
	if remote != nil {
		if data, err := remote.GetBytes(r.Context(), "/quick/Marshal.4.8/"+rel); err == nil {
			easyblobkit.OctetResponse(w, r, data)
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *State) dependencies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("gems")
	if q == "" && r.Method == http.MethodPost {
		easyblobkit.LimitBody(w, r)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		for _, pair := range strings.Split(string(body), "&") {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 && kv[0] == "gems" {
				q = kv[1]
			}
		}
	}
	b := []byte{0x04, 0x08}
	if q == "" {
		b = marshalArrayLen(b, 0)
		easyblobkit.OctetResponse(w, r, b)
		return
	}
	gems := strings.Split(q, ",")
	b = marshalArrayLen(b, len(gems))
	for _, g := range gems {
		if g == "" {
			continue
		}
		// Strip a trailing -version suffix if present: the dependency array
		// below carries only the name and a wildcard requirement.
		name := g
		if i := strings.LastIndex(g, "-"); i >= 0 {
			if cand := g[i+1:]; cand != "" && cand[0] >= '0' && cand[0] <= '9' {
				name = g[:i]
			}
		}
		b = marshalArrayLen(b, 2)
		b = marshalArrayLen(b, 3)
		b = marshalIstring(b, name)
		b = marshalRequirement(b)
		b = marshalIstring(b, "ruby")
		b = marshalArrayLen(b, 0)
	}
	easyblobkit.OctetResponse(w, r, b)
}

func (s *State) upload(w http.ResponseWriter, r *http.Request) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "rubygems") {
		return
	}
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	name, version := extractNameVersionGem(data)
	if name == "" {
		name = "unknown"
	}
	if version == "" {
		version = "0.1.0"
	}
	filename := name + "-" + version + ".gem"
	storeVersionSource(s.Registry, name, version, filename, data, "push", r.Context())
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *State) yank(w http.ResponseWriter, r *http.Request, name, version string) {
	if name == "" {
		easyblobkit.Error(w, http.StatusNotFound, "missing gem_name")
		return
	}
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "rubygems") {
		return
	}
	if version == "" {
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "rubygems", name)
		for _, v := range vs {
			removeVersion(s.Registry, name, v, r.Context())
			s.markYanked(r.Context(), name, v)
		}
	} else {
		removeVersion(s.Registry, name, version, r.Context())
		s.markYanked(r.Context(), name, version)
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// yankFromRequest reads gem_name/version from the DELETE body (url-encoded
// form) first, then the query string, matching RubyGems client behavior.
func (s *State) yankFromRequest(w http.ResponseWriter, r *http.Request) {
	name, version := "", ""
	if r.Method == http.MethodDelete {
		easyblobkit.LimitBody(w, r)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		vals, _ := url.ParseQuery(string(body))
		name = vals.Get("gem_name")
		version = vals.Get("version")
	}
	if name == "" {
		name = r.URL.Query().Get("gem_name")
		version = r.URL.Query().Get("version")
	}
	if name == "" {
		name = r.PostFormValue("gem_name")
		version = r.PostFormValue("version")
	}
	s.yank(w, r, name, version)
}

func (s *State) download(w http.ResponseWriter, r *http.Request, filename string) {
	filename = filename[strings.LastIndex(filename, "/")+1:]
	stem := strings.TrimSuffix(filename, ".gem")
	name, version := nameVersionFromStem(stem)
	if art, err := s.Registry.Meta.Get(r.Context(), "rubygems", name, version); err == nil {
		for _, b := range art.Blobs {
			if easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream") {
				return
			}
		}
	}
	// Pull-through only for gems NOT previously yanked locally. A yank
	// removes the version record, but the upstream mirror still serves it; to
	// keep yanked gems deleted we must NOT fall through to the proxy for a
	// name/version that was yanked. Track yanked names in a meta slot.
	if s.isYanked(r.Context(), name, version) {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "rubygems", Sub: "gems"}); err == nil {
		if data, err := remote.GetBytes(r.Context(), "/"+filename); err == nil {
			storeVersionSource(s.Registry, name, version, filename, data, "pull", r.Context())
			easyblobkit.OctetResponse(w, r, data)
			return
		} else {
			easyblobkit.WriteUpstreamError(w, err)
			return
		}
	}
	easyblobkit.Error(w, http.StatusNotFound, "not found")
}

// yankedVersions tracks locally yanked name@version so a delete "sticks".
const yankedMetaKey = "sg-yanked"

func yankedSet(ctx context.Context, reg *easyblobkit.Registry) map[string]bool {
	set := map[string]bool{}
	if b, err := reg.Meta.GetMeta(ctx, "rubygems", yankedMetaKey); err == nil {
		_ = json.Unmarshal(b, &set)
	}
	return set
}

func (s *State) isYanked(ctx context.Context, name, version string) bool {
	return yankedSet(ctx, s.Registry)[name+"@"+version]
}

func (s *State) markYanked(ctx context.Context, name, version string) {
	set := yankedSet(ctx, s.Registry)
	set[name+"@"+version] = true
	b, _ := json.Marshal(set)
	_ = s.Registry.Meta.SetMeta(ctx, "rubygems", yankedMetaKey, b)
}

func nameVersionFromStem(stem string) (string, string) {
	if i := strings.LastIndex(stem, "-"); i > 0 {
		return stem[:i], stem[i+1:]
	}
	return stem, "0.0.0"
}

func removeVersion(reg *easyblobkit.Registry, name, version string, ctx context.Context) {
	if art, err := reg.Meta.Get(ctx, "rubygems", name, version); err == nil {
		for _, b := range art.Blobs {
			easyblobkit.LogMetaErr("blob delete", reg.Blobs.Delete(ctx, b.Digest))
		}
	}
	easyblobkit.LogMetaErr("meta delete", reg.Meta.Delete(ctx, "rubygems", name, version))
}

func storeVersionSource(reg *easyblobkit.Registry, name, version, filename string, data []byte, source string, ctx context.Context) {
	reg.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "rubygems", Repository: name, Version: version,
		Filename: filename, Source: source, Data: data,
	})
}

// extractNameVersionGem reads metadata.gz (YAML gemspec) from a .gem archive.
func extractNameVersionGem(data []byte) (string, string) {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", ""
		}
		if hdr.Name != "metadata.gz" {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return "", ""
		}
		ym, _ := io.ReadAll(gz)
		_ = gz.Close()
		return yamlField(string(ym), "name"), yamlVersion(string(ym))
	}
	return "", ""
}

func yamlField(s, field string) string {
	prefix := field + ":"
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func yamlVersion(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "version:"); ok {
			v := strings.TrimSpace(rest)
			if v != "" && v[0] >= '0' && v[0] <= '9' {
				return v
			}
		}
	}
	return ""
}
