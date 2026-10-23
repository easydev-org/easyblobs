// Package cargo implements the cargo registry protocol: sparse index (upstream
// page rewritten + local overlay), config.json, search, publish (binary
// framing), download with pull-through, yank/unyank, owners. Mirror of the
// pkglab reference.
package cargo

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

type State struct {
	Registry *easyblobkit.Registry
	Auth     easyblobkit.Auth
	SelfBase string
}

func NewHandler(reg *easyblobkit.Registry, cfg map[string]any) (http.Handler, error) {
	s := &State{Registry: reg}
	if a, ok := cfg["auth"].(easyblobkit.Auth); ok {
		s.Auth = a
	}
	if v, ok := cfg["self_base"].(string); ok {
		s.SelfBase = v
	}
	return s, nil
}

func init() { easyblobkit.Register("cargo", NewHandler) }

type meta struct {
	Yanked map[string]bool `json:"yanked"`
	Owners []string        `json:"owners"`
}

func (s *State) base() string { return easyblobkit.SelfURL(s.SelfBase, "cargo") }

func (s *State) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/artifacts/cargo")
	path = strings.TrimPrefix(path, "/cargo")
	path = strings.Trim(path, "/")

	switch {
	case path == "config.json" || path == "index/config.json":
		s.config(w, r)
	case path == "index/config.json":
		s.config(w, r)
	case path == "me":
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"user": map[string]any{"id": 1, "login": "anonymous", "name": "anonymous"}})
	case strings.HasPrefix(path, "index/"):
		s.sparseIndex(w, r, strings.TrimPrefix(path, "index/"))
	case strings.HasPrefix(path, "api/v1/crates/new"):
		if r.Method == http.MethodPut {
			s.publish(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	case strings.HasPrefix(path, "api/v1/crates/") && strings.HasSuffix(path, "/download"):
		parts := strings.Split(strings.TrimSuffix(path, "/download"), "/")
		// api/v1/crates/{name}/{version}/download
		s.download(w, r, parts[3], parts[4])
	case strings.HasPrefix(path, "api/v1/crates/") && strings.HasSuffix(path, "/yank"):
		parts := strings.Split(strings.TrimSuffix(path, "/yank"), "/")
		s.yank(w, r, parts[3], parts[4], true)
	case strings.HasPrefix(path, "api/v1/crates/") && strings.HasSuffix(path, "/unyank"):
		parts := strings.Split(strings.TrimSuffix(path, "/unyank"), "/")
		s.yank(w, r, parts[3], parts[4], false)
	case strings.Contains(path, "/owners"):
		s.owners(w, r, path)
	case path == "api/v1/crates":
		if r.Method == http.MethodGet {
			s.search(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	case strings.HasPrefix(path, "api/v1/crates/"):
		// /api/v1/crates/{name}
		name := strings.TrimPrefix(path, "api/v1/crates/")
		s.apiFallback(w, r, name)
	default:
		// Cargo's sparse protocol appends the crate path to the registry base
		// directly (e.g. <base>/an/yh/anyhow), NOT under an `index/` prefix.
		// Serve any remaining GET as a sparse index entry.
		if r.Method == http.MethodGet {
			s.sparseIndex(w, r, strings.TrimPrefix(path, "index/"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *State) config(w http.ResponseWriter, r *http.Request) {
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"dl": s.base() + "/api/v1/crates", "api": s.base()})
}

func (s *State) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	var crates []any
	seen := map[string]bool{}
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "cargo")
	for _, name := range repos {
		if q != "" && !strings.Contains(strings.ToLower(name), q) {
			continue
		}
		versions, _ := s.Registry.Meta.ListVersions(r.Context(), "cargo", name)
		latest := easyblobkit.HighestVersion(versions)
		if latest == "" && len(versions) > 0 {
			latest = versions[len(versions)-1]
		}
		crates = append(crates, map[string]any{"name": name, "max_version": latest})
		seen[name] = true
	}
	// Merge crates.io's search so uncached public crates are discoverable.
	if remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "cargo"}); err == nil {
		p := "/api/v1/crates"
		if raw := r.URL.RawQuery; raw != "" {
			p += "?" + raw
		}
		if body, err := remote.GetBytes(r.Context(), p); err == nil {
			var up struct {
				Crates []map[string]any `json:"crates"`
			}
			if json.Unmarshal(body, &up) == nil {
				for _, m := range up.Crates {
					if n, _ := m["name"].(string); n != "" && !seen[n] {
						crates = append(crates, m)
					}
				}
			}
		}
	}
	if crates == nil {
		crates = []any{}
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"crates": crates, "meta": map[string]any{"total": len(crates)}})
}

func (s *State) apiFallback(w http.ResponseWriter, r *http.Request, name string) {
	name = strings.Trim(name, "/")
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "cargo", name)
	if len(versions) == 0 {
		remote, err := s.Registry.Remote(r.Context(), easyblobkit.UpstreamSpec{Format: "cargo"})
		if err == nil {
			if body, err := remote.GetBytes(r.Context(), "/api/v1/crates/"+easyblobkit.URLencode(name)); err == nil {
				easyblobkit.JSON(w, http.StatusOK, json.RawMessage(body))
				return
			}
		}
		easyblobkit.Error(w, http.StatusNotFound, "crate not found")
		return
	}
	m := s.loadMeta(r.Context(), name)
	var vs []any
	for _, v := range versions {
		vs = append(vs, map[string]any{
			"crate_size": 0, "num": v, "dl_path": "/api/v1/crates/" + easyblobkit.URLencode(name) + "/download",
			"yanked": m.Yanked[v],
		})
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"versions": vs})
}

func (s *State) sparseIndex(w http.ResponseWriter, r *http.Request, rel string) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		easyblobkit.Text(w, http.StatusOK, "", "text/plain")
		return
	}
	name := rel[strings.LastIndex(rel, "/")+1:]
	versions, _ := s.Registry.Meta.ListVersions(r.Context(), "cargo", name)
	easyblobkit.SortSemver(versions)

	upstreamBody := s.fetchSparseIndex(r.Context(), rel)
	if len(versions) == 0 {
		if upstreamBody != "" {
			easyblobkit.Text(w, http.StatusOK, upstreamBody, "text/plain")
			return
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}
	easyblobkit.Text(w, http.StatusOK, s.renderSparseIndex(r.Context(), name, versions, upstreamBody), "text/plain")
}

// renderSparseIndex merges the upstream sparse-index body with the locally
// cached versions. A version present BOTH upstream and locally (pull-through)
// keeps the upstream line (deps/features) with only our checksum/yank overlaid;
// a locally-published version with no upstream line gets a minimal skeleton.
func (s *State) renderSparseIndex(ctx context.Context, name string, versions []string, upstreamBody string) string {
	var upstreamLines []string
	// upstreamByVersion remembers the full upstream line for every version so a
	// locally-cached version can keep its real deps/features (see below).
	upstreamByVersion := map[string]string{}
	for _, line := range strings.Split(upstreamBody, "\n") {
		var mv map[string]any
		if json.Unmarshal([]byte(line), &mv) == nil {
			if v, ok := mv["vers"].(string); ok {
				upstreamByVersion[v] = line
				if contains(versions, v) {
					continue
				}
			}
		}
		upstreamLines = append(upstreamLines, line)
	}
	var localLines []string
	m := s.loadMeta(ctx, name)
	for _, v := range versions {
		cksum := ""
		if art, err := s.Registry.Meta.Get(ctx, "cargo", name, v); err == nil && len(art.Blobs) > 0 {
			cksum = art.Blobs[0].Hex()
		}
		// A version that is BOTH locally cached (pull-through) AND present
		// upstream must keep the upstream deps/features — only the checksum is
		// ours. Emitting an empty `deps`/`features` skeleton (the old behaviour)
		// broke every consumer that needs a crate's features (e.g. `windows`),
		// because cargo then thinks the crate declares none.
		if up, ok := upstreamByVersion[v]; ok {
			var mv map[string]any
			if json.Unmarshal([]byte(up), &mv) == nil {
				mv["cksum"] = cksum
				mv["yanked"] = m.Yanked[v]
				if b, err := json.Marshal(mv); err == nil {
					localLines = append(localLines, string(b))
					continue
				}
			}
		}
		// Locally-published version (no upstream line): synthesise the skeleton.
		entry, _ := json.Marshal(map[string]any{
			"name": name, "vers": v, "deps": []any{}, "cksum": cksum, "features": map[string]any{},
			"yanked": m.Yanked[v], "links": nil,
		})
		localLines = append(localLines, string(entry))
	}
	out := strings.Join(upstreamLines, "\n")
	if len(localLines) > 0 {
		if out != "" {
			out += "\n"
		}
		out += strings.Join(localLines, "\n")
	}
	return out + "\n"
}

func (s *State) fetchSparseIndex(ctx context.Context, rel string) string {
	remote, err := s.registrySubRemote(ctx, "cargo", "index")
	if err != nil {
		return ""
	}
	body, err := remote.GetCached(ctx, easyblobkit.SharedIndexCache(), "/"+strings.Trim(rel, "/"))
	if err != nil {
		return ""
	}
	return s.rewriteSparseIndex(body)
}

func (s *State) rewriteSparseIndex(body string) string {
	var sb strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var mv map[string]any
		if json.Unmarshal([]byte(line), &mv) == nil {
			if name, ok := mv["name"].(string); ok && name != "" {
				mv["dl"] = s.base() + "/api/v1/crates"
				b, _ := json.Marshal(mv)
				sb.WriteString(string(b))
				sb.WriteByte('\n')
				continue
			}
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (s *State) download(w http.ResponseWriter, r *http.Request, name, version string) {
	filename := name + "-" + version + ".crate"
	if art, err := s.Registry.Meta.Get(r.Context(), "cargo", name, version); err == nil {
		for _, b := range art.Blobs {
			if easyblobkit.ServeBlobAtNamed(w, r, s.Registry.Blobs, r.Context(), b.Digest, "application/octet-stream", filename) {
				return
			}
		}
	}
	remote, err := s.registrySubRemote(r.Context(), "cargo", "static")
	if err == nil {
		nameEnc := easyblobkit.URLencode(name)
		cratePath := "/" + nameEnc + "/" + name + "-" + version + ".crate"
		apiPath := "/api/v1/crates/" + nameEnc + "/" + version + "/download"
		data, err := remote.GetBytes(r.Context(), cratePath)
		if err != nil {
			data, err = remote.GetBytes(r.Context(), apiPath)
		}
		if err == nil {
			storeVersionSource(s.Registry, name, version, data, "pull", r.Context())
			easyblobkit.ServeData(w, r, s.Registry, r.Context(), data, "application/octet-stream", filename)
			return
		}
		easyblobkit.WriteUpstreamError(w, err)
		return
	}
	easyblobkit.Error(w, http.StatusNotFound, "not found")
}

func (s *State) publish(w http.ResponseWriter, r *http.Request) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "cargo") {
		return
	}
	easyblobkit.LimitBody(w, r)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return
	}
	name, version, crate := parsePublishBody(data)
	if name == "" {
		name = r.URL.Query().Get("name")
	}
	// The version takes the same path as the name. It used to be read ONLY from
	// the body's cargo framing or a JSON key, so a caller that supplied
	// "?name=...&version=1.0.0" got its name honoured and its version silently
	// replaced by the 0.1.0 default below - publishing 1.0.0 and then finding
	// 0.1.0 stored, which reads as "the registry lost my upload".
	if version == "" {
		version = r.URL.Query().Get("version")
	}
	if name == "" {
		name = jsonStr(data, "name")
		version = jsonStr(data, "version")
	}
	if version == "" {
		version = "0.1.0"
	}
	if name == "" {
		easyblobkit.Error(w, http.StatusBadRequest, "missing name")
		return
	}
	if len(crate) == 0 {
		crate = data
	}
	storeVersionSource(s.Registry, name, version, crate, "push", r.Context())
	easyblobkit.JSON(w, http.StatusCreated, map[string]any{"warnings": map[string]any{}})
}

func (s *State) yank(w http.ResponseWriter, r *http.Request, name, version string, yanked bool) {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "cargo") {
		return
	}
	if _, err := s.Registry.Meta.Get(r.Context(), "cargo", name, version); err != nil {
		easyblobkit.Error(w, http.StatusNotFound, "not found")
		return
	}
	m := s.loadMeta(r.Context(), name)
	if m.Yanked == nil {
		m.Yanked = map[string]bool{}
	}
	m.Yanked[version] = yanked
	s.saveMeta(r.Context(), name, m)
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *State) owners(w http.ResponseWriter, r *http.Request, path string) {
	full := strings.TrimPrefix(path, "api/v1/crates/")
	parts := strings.Split(full, "/")
	name := parts[0]
	// PUT/DELETE mutate the owner set; GET lists it.
	if (r.Method == http.MethodPut || r.Method == http.MethodDelete) && len(parts) >= 2 && parts[1] == "owners" {
		if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "cargo") {
			return
		}
		easyblobkit.LimitBody(w, r)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			easyblobkit.WriteReadErr(w, err)
			return
		}
		m := s.loadMeta(r.Context(), name)
		var req struct {
			Users []string `json:"users"`
		}
		if json.Unmarshal(data, &req) == nil {
			switch r.Method {
			case http.MethodPut:
				for _, u := range req.Users {
					if !contains(m.Owners, u) {
						m.Owners = append(m.Owners, u)
					}
				}
				s.saveMeta(r.Context(), name, m)
			case http.MethodDelete:
				var removed []string
				for _, u := range req.Users {
					if contains(m.Owners, u) {
						removed = append(removed, u)
					}
				}
				var kept []string
				for _, o := range m.Owners {
					if !contains(removed, o) {
						kept = append(kept, o)
					}
				}
				m.Owners = kept
				s.saveMeta(r.Context(), name, m)
			}
		}
		easyblobkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "msg": "owners updated"})
		return
	}
	m := s.loadMeta(r.Context(), name)
	var users []any
	for _, o := range m.Owners {
		users = append(users, map[string]any{"login": o, "name": o})
	}
	easyblobkit.JSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *State) loadMeta(ctx context.Context, name string) meta {
	m := meta{Yanked: map[string]bool{}}
	if art, err := s.Registry.Meta.Get(ctx, "cargo", name, ""); err == nil && len(art.Proprietary) > 0 {
		if err := json.Unmarshal(art.Proprietary, &m); err != nil {
			log.Printf("cargo: cached index for %s is malformed: %v", name, err)
		}
		if m.Yanked == nil {
			m.Yanked = map[string]bool{}
		}
	}
	return m
}

func (s *State) saveMeta(ctx context.Context, name string, m meta) {
	b, _ := json.Marshal(m)
	easyblobkit.LogMetaErr("meta put", s.Registry.Meta.Put(ctx, easyblobkit.Artifact{Format: "cargo", Repository: name, Version: "", Proprietary: b}))
}

func (s *State) registrySubRemote(ctx context.Context, format, sub string) (*easyblobkit.Remote, error) {
	return s.Registry.Remote(ctx, easyblobkit.UpstreamSpec{Format: format, Sub: sub})
}

// parsePublishBody parses cargo's binary framing:
// [u32-le jsonLen][metadata JSON][u32-le crateLen][crate bytes].
func parsePublishBody(data []byte) (name, version string, crate []byte) {
	if len(data) < 8 {
		return "", "", nil
	}
	jsonLen := binary.LittleEndian.Uint32(data[0:4])
	if int(jsonLen) == 0 || 4+int(jsonLen) > len(data) {
		return "", "", nil
	}
	var mv map[string]any
	if json.Unmarshal(data[4:4+int(jsonLen)], &mv) != nil {
		return "", "", nil
	}
	name, _ = mv["name"].(string)
	version, _ = mv["vers"].(string)
	cratePos := 4 + int(jsonLen)
	if cratePos+4 > len(data) {
		return name, version, nil
	}
	crateLen := binary.LittleEndian.Uint32(data[cratePos : cratePos+4])
	end := cratePos + 4 + int(crateLen)
	if end > len(data) {
		end = len(data)
	}
	return name, version, data[cratePos+4 : end]
}

func storeVersionSource(reg *easyblobkit.Registry, name, version string, data []byte, source string, ctx context.Context) {
	if version == "" {
		version = "0.1.0"
	}
	reg.StoreVersion(ctx, easyblobkit.VersionInput{
		Format: "cargo", Repository: name, Version: version,
		Filename: name + "-" + version + ".crate", Source: source, Data: data,
	})
}

func jsonStr(data []byte, key string) string {
	var m map[string]any
	if json.Unmarshal(data, &m) == nil {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var errNoUpstream = errorString("no upstream")

type errorString string

func (e errorString) Error() string { return string(e) }
