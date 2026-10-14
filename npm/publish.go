package npm

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
	var version string
	var pkgJSON map[string]any
	if vs, ok := payload["versions"].(map[string]any); ok {
		for k, v := range vs {
			version = k
			pkgJSON, _ = v.(map[string]any)
			break
		}
	}
	if version == "" {
		if dt, ok := payload["dist-tags"].(map[string]any); ok {
			version, _ = dt["latest"].(string)
		}
	}
	if version == "" {
		easyblobkit.Error(w, http.StatusBadRequest, "cannot determine version")
		return
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
	s.storeVersion(r.Context(), pkgName, version, tarball, pkgJSON, "push")
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *State) storeVersion(ctx context.Context, name, version string, tarball []byte, pkgJSON map[string]any, source string) {
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
	s.Registry.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "npm", Repository: name, Version: version,
		MediaType: "application/json", Filename: tarballFilename(name, version),
		Source: source, Data: tarball, Proprietary: prop,
	})
}

func hexBytes(s string) []byte {
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}
