package npm

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) metadata(w http.ResponseWriter, r *http.Request, name string) {
	if !easyblobkit.AuthorizeReadFor(w, r, s.Auth, s.Registry, "npm", name) {
		return
	}
	if body := s.aggregateMetadata(r, name); body != "" {
		easyblobkit.JSON(w, http.StatusOK, json.RawMessage(body))
		return
	}
	remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "npm"})
	if err != nil {
		easyblobkit.Error(w, http.StatusNotFound, "package not found")
		return
	}
	body, err := remote.GetCached(r.Context(), easyblobkit.SharedIndexCache(), "/"+encodeName(name))
	if err != nil {
		easyblobkit.Error(w, http.StatusNotFound, "package not found")
		return
	}
	easyblobkit.JSON(w, http.StatusOK, json.RawMessage(s.rewriteTarballURLs(r, body, name)))
}

func (s *State) aggregateMetadata(r *http.Request, name string) string {
	ctx := r.Context()
	versions, _ := s.Registry.Meta.ListVersions(ctx, "npm", name)
	if len(versions) == 0 {
		return ""
	}
	vmap := map[string]any{}
	// Local versions are authoritative; but the resolver still needs the FULL
	// version list to satisfy ranges (e.g. is-even -> is-odd@^0.1.2 while only
	// 3.0.1 is cached). Overlay upstream versions (tarballs rewritten to self)
	// beneath them, then re-apply local metadata over the same version.
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "npm"}); err == nil {
		if body, err := remote.GetCached(ctx, easyblobkit.SharedIndexCache(), "/"+encodeName(name)); err == nil {
			var up map[string]any
			if json.Unmarshal([]byte(body), &up) == nil {
				if uv, ok := up["versions"].(map[string]any); ok {
					for ver, info := range uv {
						if _, exists := vmap[ver]; exists {
							continue
						}
						if m, ok := info.(map[string]any); ok {
							if dist, ok := m["dist"].(map[string]any); ok {
								dist["tarball"] = s.tarballURLCtx(r, name, ver)
							}
							vmap[ver] = m
						}
					}
				}
			}
		}
	}
	// Re-apply local metadata (authoritative) over everything.
	for _, v := range versions {
		art, err := s.Registry.Meta.Get(ctx, "npm", name, v)
		if err != nil {
			continue
		}
		pj := map[string]any{}
		_ = json.Unmarshal(art.Proprietary, &pj)
		if _, ok := pj["name"]; !ok {
			pj["name"] = name
		}
		if _, ok := pj["version"]; !ok {
			pj["version"] = v
		}
		dist := map[string]any{"tarball": s.tarballURLCtx(r, name, v)}
		for _, b := range art.Blobs {
			if b.Digest == "" {
				continue
			}
			h, err := s.Registry.BlobHashes(ctx, b.Digest)
			if err == nil {
				dist["shasum"] = h.SHA1
				intB64 := base64.StdEncoding.EncodeToString(hexBytes(h.SHA512))
				dist["integrity"] = "sha512-" + intB64
			}
			if b.Size > 0 {
				dist["size"] = b.Size
			}
		}
		// MERGE the local metadata OVER the upstream entry (if we have one for
		// this version) instead of REPLACING it. The local record's Proprietary
		// is the package's OWN package.json (name/version/…), which LACKS the
		// upstream-only fields the resolver needs — notably `dependencies` /
		// `peerDependencies`. Replacing dropped them, so `npm install` of a
		// version whose tarball we host (but whose packument came from upstream)
		// failed with "Cannot find package <dep>" (the dep was never installed
		// because the range was absent). Keep the upstream object as the base
		// and overlay only the locally-authoritative fields.
		merged := map[string]any{}
		if base, ok := vmap[v].(map[string]any); ok {
			for k, val := range base {
				merged[k] = val
			}
		}
		for k, val := range pj {
			merged[k] = val
		}
		merged["name"] = name
		merged["version"] = v
		merged["dist"] = dist
		vmap[v] = merged
	}
	latest := easyblobkit.HighestVersion(versions)
	if latest == "" && len(versions) > 0 {
		latest = versions[len(versions)-1]
	}
	dt := s.distTagsMap(ctx, name)
	if _, ok := dt["latest"]; !ok {
		dt["latest"] = latest
	}
	doc := map[string]any{"_id": name, "_rev": "1", "name": name, "dist-tags": dt, "versions": vmap}
	b, _ := json.Marshal(doc)
	return string(b)
}

// tarballURL builds the dist.tarball for one version. When the request targets
// an explicit repository (the /-/<repo>/ admin form), the URL carries that
// repository so the client's download lands in the same namespace; a native
// request keeps the plain shape the client already uses.
func (s *State) tarballURLCtx(r *http.Request, name, version string) string {
	base := s.base(r)
	if sc := easyblobkit.RepoScopeFrom(r.Context()); sc.Explicit && sc.Namespace != "" {
		base += "/-/" + sc.Namespace
	}
	// Preserve an explicit "/_upstream/<host>/" segment. The packument for
	// @jsr/* packages comes from a named target (npm.jsr.io); if the tarball
	// URL drops that segment the client asks the DEFAULT npm upstream for the
	// bytes, which is a different repository entirely - and in an isolated
	// deployment that request simply fails. The scope records the host, so
	// re-emit it here.
	if sc := easyblobkit.RepoScopeFrom(r.Context()); sc.Upstream != "" {
		base += "/_upstream/" + sc.Upstream
	}
	return base + "/" + encodeName(name) + "/-/" + tarballFilename(name, version)
}

func (s *State) rewriteTarballURLs(r *http.Request, packument, name string) string {
	var doc map[string]any
	if json.Unmarshal([]byte(packument), &doc) != nil {
		return packument
	}
	if versions, ok := doc["versions"].(map[string]any); ok {
		for ver, info := range versions {
			if m, ok := info.(map[string]any); ok {
				if dist, ok := m["dist"].(map[string]any); ok {
					dist["tarball"] = s.tarballURLCtx(r, name, ver)
				}
			}
		}
	}
	b, _ := json.Marshal(doc)
	return string(b)
}
