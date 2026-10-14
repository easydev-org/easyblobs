package conan

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) loadRecipeRev(ctx context.Context, name, ver string) string {
	if rev, ok := loadRecipeRevOf(ctx, s.Registry, name, ver); ok {
		return rev
	}
	if rev, ok := loadRecipeRevOf(ctx, s.Registry, name, ""); ok {
		return rev
	}
	return "0"
}

// loadFile resolves a recipe (or package) file by walking the recorded
// artifacts whose version matches the filename (recipe files) — exact match.
func (s *State) loadFile(ctx context.Context, name, ver, filename string) ([]byte, bool) {
	if art, err := s.Registry.Meta.Get(ctx, "conan", name, filename); err == nil && len(art.Blobs) > 0 {
		rd, err := s.Registry.Blobs.Open(ctx, art.Blobs[0].Digest)
		if err != nil || rd == nil {
			return nil, false
		}
		data, _ := io.ReadAll(rd)
		_ = rd.Close()
		return data, true
	}
	// Fall back: any recorded artifact blob named this filename.
	vs, err := s.Registry.Meta.ListVersions(ctx, "conan", name)
	if err != nil {
		return nil, false
	}
	for _, v := range vs {
		if art, err := s.Registry.Meta.Get(ctx, "conan", name, v); err == nil {
			for _, b := range art.Blobs {
				if b.Name == filename {
					rd, err := s.Registry.Blobs.Open(ctx, b.Digest)
					if err != nil || rd == nil {
						continue
					}
					data, _ := io.ReadAll(rd)
					_ = rd.Close()
					return data, true
				}
			}
		}
	}
	return nil, false
}

// packageFiles returns the package-file names for a pid as a filename->URL
// dict (Conan's package /files listing shape).
func (s *State) packageFiles(ctx context.Context, name string, parts []string) map[string]any {
	pid := ""
	for i, p := range parts {
		if p == "packages" && i+1 < len(parts) {
			pid = parts[i+1]
			break
		}
	}
	if pid == "" {
		return map[string]any{}
	}
	vs, _ := s.Registry.Meta.ListVersions(ctx, "conan", name)
	base := s.base()
	out := map[string]any{}
	for _, v := range vs {
		if !strings.HasPrefix(v, "package/"+pid+"/") {
			continue
		}
		fname := v[strings.LastIndex(v, "/")+1:]
		url := base + "/v2/conans/" + easyblobkit.URLencode(name) + "/_/_/_/_/revisions/0/packages/" + easyblobkit.URLencode(pid) + "/revisions/0/files/" + easyblobkit.URLencode(fname)
		out[fname] = url
	}
	return out
}

// packageSearch returns a locally-published recipe's package binaries in the
// shape Conan's /search endpoint expects: pid -> configuration. Only the
// settings/options/content a pid's stored conaninfo.txt records are surfaced;
// the list is enough for the client to then fetch the package files.
func (s *State) packageSearch(ctx context.Context, name string) map[string]any {
	vs, _ := s.Registry.Meta.ListVersions(ctx, "conan", name)
	out := map[string]any{}
	for _, v := range vs {
		if !strings.HasPrefix(v, "package/") {
			continue
		}
		rest := strings.TrimPrefix(v, "package/")
		pid := strings.SplitN(rest, "/", 2)[0]
		if pid == "" {
			continue
		}
		if _, seen := out[pid]; seen {
			continue
		}
		info := map[string]any{"settings": map[string]any{}, "options": map[string]any{}, "content": "", "requires": []any{}}
		// The conaninfo.txt lives under the same pid slot: replace the
		// filename with "conaninfo.txt" ("package/{pid}/{prev}/conaninfo.txt").
		if i := strings.LastIndex(v, "/"); i > 0 {
			infoKey := v[:i] + "/conaninfo.txt"
			if data, ok := s.loadFile(ctx, name, infoKey, infoKey); ok {
				settings, options, content := parseConaninfo(string(data))
				info["settings"], info["options"], info["content"] = settings, options, content
			}
		}
		out[pid] = info
	}
	return out
}

// parseConaninfo extracts the [settings]/[options] maps and the raw content
// from a conaninfo.txt body.
func parseConaninfo(body string) (map[string]any, map[string]any, string) {
	settings := map[string]any{}
	options := map[string]any{}
	section := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch section {
		case "settings":
			settings[k] = v
		case "options":
			options[k] = v
		}
	}
	return settings, options, body
}

// packageRev returns the recorded package revision for a pid if present.
func (s *State) packageRev(ctx context.Context, name, ver string, parts []string) (string, bool) {
	// locate pid: index of "packages" in parts
	pid := ""
	for i, p := range parts {
		if p == "packages" && i+1 < len(parts) {
			pid = parts[i+1]
			break
		}
	}
	if pid == "" {
		return "", false
	}
	// package files are stored as art under (conan, name, "package/{pid}/{prev}/{file}")
	// We store package files under name+"/"+pid+"_"+filename; reconstruct the
	// prev from the recorded artifacts' version prefix.
	vs, err := s.Registry.Meta.ListVersions(ctx, "conan", name)
	if err != nil {
		return "", false
	}
	out := ""
	for _, v := range vs {
		if strings.HasPrefix(v, "package/"+pid+"/") {
			// v = "package/{pid}/{prev}/{file}"
			rest := strings.TrimPrefix(v, "package/"+pid+"/")
			if prev := strings.SplitN(rest, "/", 2)[0]; prev != "" {
				out = prev
			}
		}
	}
	if out == "" {
		return "", false
	}
	return out, true
}

// recipeExists reports whether a recipe version was uploaded locally.
func (s *State) recipeExists(ctx context.Context, name, ver string) bool {
	if _, err := s.Registry.Meta.Get(ctx, "conan", name, ver); err == nil {
		return true
	}
	// Recipe files are recorded under (name, <filename>); if any exist for
	// this recipe the recipe version is considered present.
	vs, err := s.Registry.Meta.ListVersions(ctx, "conan", name)
	if err != nil {
		return false
	}
	return len(vs) > 0
}

// recipeKnown reports whether this recipe has any local trace (metadata or a
// recorded file); unknown recipes are served straight from upstream.
func (s *State) recipeKnown(ctx context.Context, name, ver string) bool {
	return s.recipeExists(ctx, name, ver)
}

// recorded files (Conan's /files response is a dict keyed by filename).
func (s *State) recipeFiles(ctx context.Context, name string) map[string]any {
	vs, err := s.Registry.Meta.ListVersions(ctx, "conan", name)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for _, v := range vs {
		// Recipe file artifacts have their filename as the version.
		if v == "" {
			continue
		}
		if _, err := s.Registry.Meta.Get(ctx, "conan", name, v); err == nil {
			out[v] = s.base() + "/v2/conans/" + easyblobkit.URLencode(name) + "/0.0.0/ci/stable/revisions/0/files/" + easyblobkit.URLencode(v)
		}
	}
	return out
}

func storeFile(reg *easyblobkit.Registry, name, ver, filename string, data []byte, ctx context.Context) {
	// Store the file body as a blob and index it under (conan, name, filename)
	// so loadFile can find it; keep the recipe version as metadata. An empty
	// file is legitimate content (a package with no files has a 0-byte
	// conaninfo.txt) and must still be recorded, or the client reports the
	// package as corrupted.
	stored, _, err := reg.Blobs.Put(ctx, bytes.NewReader(data), "")
	if err != nil {
		return
	}
	easyblobkit.LogMetaErr("meta put", reg.Meta.Put(ctx, easyblobkit.Artifact{Format: "conan", Repository: name, Version: filename, Source: "push", Blobs: []easyblobkit.Descriptor{{Digest: stored.Digest, Size: stored.Size, Name: filename}}}))
}

func removeVersion(reg *easyblobkit.Registry, name, ver string, ctx context.Context) {
	easyblobkit.LogMetaErr("meta delete", reg.Meta.Delete(ctx, "conan", name, ver))
}

// storeRecipeRev records a recipe version plus the client-supplied recipe
// revision under (conan, name, ver) with no blobs. That makes Meta.Get find
// the version (so /latest and /revisions resolve) and preserves the revision
// for later listings.
func storeRecipeRev(reg *easyblobkit.Registry, name, ver, rev string, ctx context.Context) {
	if ver == "" {
		return
	}
	if rev == "" {
		rev = "0"
	}
	prop, _ := json.Marshal(map[string]any{"revision": rev})
	easyblobkit.LogMetaErr("meta put", reg.Meta.Put(ctx, easyblobkit.Artifact{
		Format: "conan", Repository: name, Version: ver, Source: "push", Proprietary: prop,
	}))
}

func loadRecipeRevOf(ctx context.Context, reg *easyblobkit.Registry, name, ver string) (string, bool) {
	if art, err := reg.Meta.Get(ctx, "conan", name, ver); err == nil && len(art.Proprietary) > 0 {
		var m map[string]any
		if json.Unmarshal(art.Proprietary, &m) == nil {
			if r, ok := m["revision"].(string); ok && r != "" {
				return r, true
			}
		}
	}
	return "", false
}
