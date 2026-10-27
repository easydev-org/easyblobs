package httpcache

// JSR's native registry protocol, generated from a hosted upload.
//
// DENO ASKS FOR THREE THINGS, and a hosted repo that cannot answer all three is
// not a repository a client can install from:
//
//   GET /@scope/pkg/meta.json            scope, name, latest, versions{}
//   GET /@scope/pkg/<ver>_meta.json      manifest{path:{size,checksum}},
//                                        moduleGraph1{}, exports{}
//   GET /@scope/pkg/<ver>/<path>         the module's bytes
//
// The checksum form is "sha256-<hex>" and the path keys are rooted ("/main.ts").
// Both were read off jsr.io itself rather than guessed.
//
// WHY THIS IS GENERATED AND NOT UPLOADED: the operator publishes a package
// (a deno.json plus sources); the registry documents that describe it are
// derived from those bytes. Hand-writing meta.json alongside the sources would
// let the two disagree, and the client trusts meta.json.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// jsrPackage is what one hosted upload describes: a scope, a name, a version
// and the files under it.
//
// Uploads are addressed as <scope>/<name>/<version>/<path>, e.g.
//
//	PUT /_hosted/<repo>/luca/flag/1.0.1/main.ts
//	PUT /_hosted/<repo>/luca/flag/1.0.1/deno.json
//
// so the version is a path segment rather than something to parse out of a
// tarball. That keeps the write side simple and matches how the read side
// addresses modules.
type jsrPackage struct {
	Scope   string
	Name    string
	Version string
	// Files maps the module path ("/main.ts") to its bytes.
	Files map[string][]byte
}

// jsrKey parses a hosted file name into its package and module path.
// Returns ok=false for names that are not <scope>/<name>/<version>/<path>.
func jsrKey(name string) (scope, pkg, version, modulePath string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(name, "/"), "/")
	if len(parts) < 4 {
		return "", "", "", "", false
	}
	scope, pkg, version = parts[0], parts[1], parts[2]
	if scope == "" || pkg == "" || version == "" {
		return "", "", "", "", false
	}
	modulePath = "/" + strings.Join(parts[3:], "/")
	return scope, pkg, version, modulePath, true
}

// jsrRef is one hosted file destined for a package.
type jsrRef struct {
	Path   string
	Digest string
}

// loadJSR groups the repo's files by package and reads their bytes, so the
// generator can render every registry document in one pass.
func loadJSR(ctx context.Context, store easyblobkit.HostedStore,
	files []easyblobkit.HostedFile) (map[string]*jsrPackage, error) {

	type group struct {
		pkg  *jsrPackage
		refs []jsrRef
	}
	groups := map[string]*group{}
	for _, f := range files {
		scope, pkg, version, mod, ok := jsrKey(f.Name)
		if !ok {
			continue
		}
		id := "@" + scope + "/" + pkg + "@" + version
		g := groups[id]
		if g == nil {
			g = &group{pkg: &jsrPackage{
				Scope: scope, Name: pkg, Version: version,
				Files: map[string][]byte{},
			}}
			groups[id] = g
		}
		g.refs = append(g.refs, jsrRef{Path: mod, Digest: f.Digest})
	}

	out := map[string]*jsrPackage{}
	for id, g := range groups {
		for _, r := range g.refs {
			rd, err := store.Registry.Blobs.Open(ctx, r.Digest)
			if err != nil || rd == nil {
				return nil, fmt.Errorf("blob %s unavailable", r.Digest)
			}
			data, err := io.ReadAll(rd)
			_ = rd.Close()
			if err != nil {
				return nil, err
			}
			g.pkg.Files[r.Path] = data
		}
		out[id] = g.pkg
	}
	return out, nil
}

// jsrChecksum is the "sha256-<hex>" form JSR uses in manifest entries.
func jsrChecksum(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256-" + hex.EncodeToString(sum[:])
}

// jsrMeta renders /@scope/pkg/meta.json: every version.
func jsrMeta(scope, name string, versions []string) []byte {
	sortedVersions := append([]string(nil), versions...)
	sort.Strings(sortedVersions)
	vs := make(map[string]any, len(sortedVersions))
	for _, v := range sortedVersions {
		vs[v] = map[string]any{}
	}
	latest := ""
	if len(sortedVersions) > 0 {
		latest = sortedVersions[len(sortedVersions)-1]
	}
	doc := map[string]any{
		"scope":    scope,
		"name":     name,
		"latest":   latest,
		"versions": vs,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// jsrVersionMeta renders /@scope/pkg/<ver>_meta.json: the file manifest, the
// module graph and the export map deno uses to resolve the entry point.
func jsrVersionMeta(pg *jsrPackage) []byte {
	paths := make([]string, 0, len(pg.Files))
	for p := range pg.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	manifest := make(map[string]any, len(paths))
	for _, p := range paths {
		manifest[p] = map[string]any{
			"size":     len(pg.Files[p]),
			"checksum": jsrChecksum(pg.Files[p]),
		}
	}

	graph := map[string]any{}
	for _, p := range paths {
		if isJSRModule(p) {
			graph[p] = map[string]any{}
		}
	}

	doc := map[string]any{
		"manifest":     manifest,
		"moduleGraph1": graph,
		"exports":      jsrExports(pg.Files),
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

// isJSRModule reports whether a path is source deno should parse. The real
// registry lists README/LICENSE/deno.json in the manifest but not in the graph.
func isJSRModule(p string) bool {
	base := strings.ToLower(strings.TrimPrefix(p, "/"))
	switch base {
	case "readme.md", "license", "license.md", "deno.json", "deno.jsonc":
		return false
	}
	return strings.HasSuffix(p, ".ts") || strings.HasSuffix(p, ".js") ||
		strings.HasSuffix(p, ".mjs") || strings.HasSuffix(p, ".tsx") ||
		strings.HasSuffix(p, ".jsx")
}

// jsrExports reads the export map from the package's deno.json when it has one,
// and otherwise points "." at a conventional entry point.
//
// deno resolves the bare specifier through this map, so a wrong one means
// "package found, nothing to import".
func jsrExports(files map[string][]byte) map[string]string {
	if raw, ok := files["/deno.json"]; ok {
		var dj struct {
			Exports json.RawMessage `json:"exports"`
		}
		if json.Unmarshal(raw, &dj) == nil && len(dj.Exports) > 0 {
			var one string
			if json.Unmarshal(dj.Exports, &one) == nil && one != "" {
				return map[string]string{".": one}
			}
			var many map[string]string
			if json.Unmarshal(dj.Exports, &many) == nil && len(many) > 0 {
				return many
			}
		}
	}
	for _, cand := range []string{"/mod.ts", "/main.ts", "/src/mod.ts", "/index.ts"} {
		if _, ok := files[cand]; ok {
			return map[string]string{".": "." + cand}
		}
	}
	return map[string]string{}
}

// generateJSR renders every registry document the repo's packages imply:
//
//	@scope/pkg/meta.json
//	@scope/pkg/<ver>_meta.json
//
// The MODULE bytes are not generated - they are the uploaded files, served at
// @scope/pkg/<ver>/<path> by the normal hosted read path. Only the two document
// shapes deno synthesises nothing for are produced here.
func generateJSR(ctx context.Context, store easyblobkit.HostedStore,
	files []easyblobkit.HostedFile) (map[string]easyblobkit.GeneratedFile, error) {

	pkgs, err := loadJSR(ctx, store, files)
	if err != nil {
		return nil, err
	}

	// <scope>/<name> -> the versions that exist
	versions := map[string][]string{}
	for _, pg := range pkgs {
		base := pg.Scope + "/" + pg.Name
		versions[base] = append(versions[base], pg.Version)
	}

	out := map[string]easyblobkit.GeneratedFile{}
	for _, pg := range pkgs {
		key := "@" + pg.Scope + "/" + pg.Name
		out[key+"/"+pg.Version+"_meta.json"] = easyblobkit.GeneratedFile{
			Body:        jsrVersionMeta(pg),
			ContentType: "application/json",
		}
	}
	for base, vs := range versions {
		parts := strings.SplitN(base, "/", 2)
		if len(parts) != 2 {
			continue
		}
		out["@"+base+"/meta.json"] = easyblobkit.GeneratedFile{
			Body:        jsrMeta(parts[0], parts[1], vs),
			ContentType: "application/json",
		}
	}
	return out, nil
}
