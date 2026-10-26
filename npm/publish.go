package npm

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/easydev-org/easyblobs/core"
)

// authorizePublish is the npm publish gate: write-level credential, npm
// scope requirement, and registry ownership. npm names MUST carry a scope
// (@org/name) — unscoped global names cannot be claimed by any single
// tenant, so they are refused outright (prevents tenant squatting of
// public-name look-alikes).
func (s *State) authorizePublish(w http.ResponseWriter, r *http.Request, name string) bool {
	if !strings.HasPrefix(name, "@") || !strings.Contains(name, "/") {
		easyblobkit.JSON(w, http.StatusForbidden, map[string]any{
			"ok":    false,
			"error": "scoped package names (@scope/name) are required on this registry",
		})
		return false
	}
	return easyblobkit.AuthorizeWriteFor(w, r, s.Auth, s.Registry, "npm", name)
}

func (s *State) publish(w http.ResponseWriter, r *http.Request, name string) {
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	// Raw tarball (gzip magic).
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		ver, pkgJSON := parseTarballPackageJSON(data)
		pkgName := name
		if v, ok := pkgJSON["name"].(string); ok && v != "" {
			pkgName = v
		}
		s.storeVersion(r.Context(), pkgName, ver, data, pkgJSON, "push")
		easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
		return
	}
	// CouchDB-style publish document.
	var payload map[string]any
	if json.Unmarshal(data, &payload) != nil {
		easyblobkit.Error(w, http.StatusBadRequest, "invalid payload")
		return
	}
	pkgName := name
	if v, ok := payload["name"].(string); ok && v != "" {
		pkgName = v
	}
	// The document may carry ONE version (npm publish) or EVERY version (npm
	// deprecate, dist-tag and anything else that re-PUTs the packument it just
	// read with ?write=true). Keep them all.
	//
	// Keeping one and breaking was a real defect: `npm deprecate @scope/x@1.0.0`
	// on a package that also had 2.0.0 PUT the whole document, publish() recorded
	// a single version, and the "deprecated" flag - which lives on the version
	// being deprecated - was dropped, while the call still answered 201.
	versions, _ := payload["versions"].(map[string]any)
	if len(versions) == 0 {
		// Older clients send a bare version plus dist-tags.
		version := ""
		if dt, ok := payload["dist-tags"].(map[string]any); ok {
			version, _ = dt["latest"].(string)
		}
		if version == "" {
			easyblobkit.Error(w, http.StatusBadRequest, "cannot determine version")
			return
		}
		versions = map[string]any{version: map[string]any{"version": version}}
	}
	var tarball []byte
	if atts, ok := payload["_attachments"].(map[string]any); ok {
		for _, v := range atts {
			if m, ok := v.(map[string]any); ok {
				if b64, ok := m["data"].(string); ok {
					if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
						tarball = decoded
						break
					}
				}
			}
		}
	}
	stored := 0
	for ver, raw := range versions {
		ver = strings.TrimSpace(ver)
		if ver == "" {
			// A document with an empty key is not a version; recording one would
			// put a nameless entry in the packument (observed as "‹empty›,1.0.0,
			// 2.0.0" after a deprecate).
			continue
		}
		pkgJSON, _ := raw.(map[string]any)
		if tarball == nil {
			// No payload attached: this is a metadata-only update (deprecate,
			// dist-tag). Apply it to the version that already exists instead of
			// storing an empty artefact over it.
			if s.updateVersionMeta(r.Context(), pkgName, ver, pkgJSON) {
				stored++
			}
			continue
		}
		s.storeVersion(r.Context(), pkgName, ver, tarball, pkgJSON, "push")
		stored++
	}
	if stored == 0 {
		easyblobkit.Error(w, http.StatusBadRequest, "no version could be stored")
		return
	}
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

// updateVersionMeta rewrites an existing version's metadata without touching its
// artefact, and reports whether a version was there to update.
//
// A metadata-only PUT (npm deprecate, npm dist-tag) carries no _attachments, so
// treating it as a publish would replace a good tarball with nothing. It also
// carries every version, so an unknown one is not an error - it just is not
// something this document can create.
func (s *State) updateVersionMeta(ctx context.Context, name, version string, pkgJSON map[string]any) bool {
	art, err := s.Registry.Meta.Get(ctx, "npm", name, version)
	if err != nil {
		return false
	}
	cur := map[string]any{}
	if len(art.Proprietary) > 0 {
		_ = json.Unmarshal(art.Proprietary, &cur)
	}
	for k, v := range pkgJSON {
		if k == "dist" {
			// dist carries the tarball URL and integrity computed for a fresh
			// upload; the stored artefact decides those here.
			continue
		}
		cur[k] = v
	}
	art.Proprietary, _ = json.Marshal(cur)
	easyblobkit.LogMetaErr("meta put", s.Registry.Meta.Put(ctx, art))
	return true
}

func (s *State) storeVersion(ctx context.Context, name, version string, tarball []byte, pkgJSON map[string]any, source string) {
	s.storeVersionAt(ctx, name, version, tarball, pkgJSON, source, time.Time{})
}

// storeVersionAt is storeVersion with an explicit publish time (from the
// packument's `time` map on a pull-through); zero falls back to now.
func (s *State) storeVersionAt(ctx context.Context, name, version string, tarball []byte, pkgJSON map[string]any, source string, publishedAt time.Time) {
	if name == "" {
		name = "unknown"
	}
	if version == "" {
		version = "0.0.0"
	}
	var prop []byte
	if pkgJSON != nil {
		prop, _ = json.Marshal(pkgJSON)
	}
	if publishedAt.IsZero() {
		publishedAt = time.Now().UTC()
	}
	s.Registry.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "npm", Repository: name, Version: version,
		MediaType: "application/json", Filename: tarballFilename(name, version),
		Source: source, Data: tarball, Proprietary: prop,
		PublishedAt: publishedAt,
	})
}

func hexBytes(s string) []byte {
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}
