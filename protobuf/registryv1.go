package protobuf

// The STABLE BSR API: buf.registry.module.v1.
//
// buf 1.4x does NOT use the alpha API for pushing. Its own error said so:
//
//	Failure: unimplemented: 404 Not Found for
//	  https://<host>/buf.registry.module.v1.ModuleService/GetModules
//
// The v1 surface is small and is taken from bufbuild/registry-proto (the public,
// stable API the CLI and web app both depend on):
//
//	service ModuleService {
//	  rpc GetModules(GetModulesRequest) returns (GetModulesResponse);
//	  rpc CreateModules(CreateModulesRequest) returns (CreateModulesResponse);
//	}
//	service UploadService {
//	  rpc Upload(UploadRequest) returns (UploadResponse);
//	}
//
//	message ModuleRef { oneof value { string id = 1; Name name = 2; } }
//	message ModuleRef.Name { string owner = 1; string module = 2; }
//
//	message UploadRequest {
//	  message Content {
//	    ModuleRef module_ref = 1;
//	    repeated File files = 2;              // File { string path; bytes content; }
//	    repeated ScopedLabelRef scoped_label_refs = 3;
//	    string source_control_url = 4;
//	  }
//	  repeated Content contents = 1;
//	}
//	message UploadResponse { repeated Commit commits = 1; }
//
//	message Commit { string id; Digest digest; ... }
//
// A buf push is therefore: GetModules (does it exist?) -> CreateModules (if
// allowed) -> Upload (the files). The mirror answers all three, so the real
// client can publish into it.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// v1Paths maps the service+method of a buf.registry.module.v1 call.
func v1ServiceMethod(path string) (svc, method string, ok bool) {
	for _, s := range []string{
		"buf.registry.module.v1.ModuleService",
		"buf.registry.module.v1.UploadService",
		"buf.registry.module.v1.CommitService",
		"buf.registry.module.v1.DownloadService",
		"buf.registry.module.v1.LabelService",
	} {
		if strings.HasPrefix(path, s+"/") {
			return s[strings.LastIndex(s, ".")+1:], strings.TrimPrefix(path, s+"/"), true
		}
	}
	return "", "", false
}

// serveRegistryV1 answers the stable API. It returns true when it handled the
// call, false to let the passthrough try the upstream.
func (s *State) serveRegistryV1(w http.ResponseWriter, r *http.Request, path string) bool {
	svc, method, ok := v1ServiceMethod(path)
	if !ok {
		return false
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "POST, GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}

	var body []byte
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxBSRBody))
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			return true
		}
		body = b
	}

	switch svc + "/" + method {
	case "ModuleService/GetModules":
		return s.v1GetModules(w, r, body)
	case "ModuleService/CreateModules":
		return s.v1CreateModules(w, body, r)
	case "UploadService/Upload":
		return s.v1Upload(w, body, r)
	case "CommitService/GetCommits":
		return s.v1GetCommits(w, r, body)
	}
	// Anything else is not implemented here: fall through to the upstream so a
	// deployment proxying a real BSR keeps working.
	return false
}

// v1ModuleRef is ModuleRef{ oneof value { id, name } } in JSON form.
type v1ModuleRef struct {
	Value struct {
		ID   string `json:"id"`
		Name struct {
			Owner  string `json:"owner"`
			Module string `json:"module"`
		} `json:"name"`
	} `json:"value"`
}

// moduleID builds the key this registry stores a module under.
func moduleID(owner, module string) string { return owner + "/" + module }

// v1ModuleJSON renders a Module message. The v1 API requires id / owner_id /
// create_time / update_time / default_label_name, so a plausible object is
// returned rather than a stub - a client validates the shape.
func v1ModuleJSON(owner, module string) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	return map[string]any{
		"id":               owner + "-" + module,
		"name":             module,
		"ownerId":          owner,
		"createTime":       now,
		"updateTime":       now,
		"visibility":       1, // MODULE_VISIBILITY_PUBLIC
		"state":            1,
		"defaultLabelName": "main",
		"description":      "published through easyblobs",
	}
}

// v1GetModules answers "does this module exist?".
//
// A module exists when this registry holds at least one pushed commit for it.
// When it does not, the response must be EMPTY (not an error): that is what
// tells buf to create it.
func (s *State) v1GetModules(w http.ResponseWriter, r *http.Request, body []byte) bool {
	var req struct {
		ModuleRefs []v1ModuleRef `json:"moduleRefs"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
		return true
	}
	mods := make([]map[string]any, 0, len(req.ModuleRefs))
	for _, ref := range req.ModuleRefs {
		owner, module := ref.Value.Name.Owner, ref.Value.Name.Module
		if owner == "" || module == "" {
			continue
		}
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "protobuf", moduleID(owner, module))
		if len(vs) == 0 {
			continue
		}
		mods = append(mods, v1ModuleJSON(owner, module))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"modules": mods})
	return true
}

// v1CreateModules registers the module's name. Nothing else is needed: the
// files arrive through Upload.
func (s *State) v1CreateModules(w http.ResponseWriter, body []byte, r *http.Request) bool {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "protobuf") {
		return true
	}
	var req struct {
		Values []struct {
			Name struct {
				Owner  string `json:"owner"`
				Module string `json:"module"`
			} `json:"name"`
			Visibility int `json:"visibility"`
		} `json:"values"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
		return true
	}
	mods := make([]map[string]any, 0, len(req.Values))
	for _, v := range req.Values {
		if v.Name.Owner == "" || v.Name.Module == "" {
			connectError(w, http.StatusBadRequest, "invalid_argument", "module name is required")
			return true
		}
		mods = append(mods, v1ModuleJSON(v.Name.Owner, v.Name.Module))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"modules": mods})
	return true
}

// v1Upload stores the module's files - this is what `buf push` actually sends.
func (s *State) v1Upload(w http.ResponseWriter, body []byte, r *http.Request) bool {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "protobuf") {
		return true
	}
	var req struct {
		Contents []struct {
			ModuleRef *v1ModuleRef `json:"moduleRef"`
			Files     []struct {
				Path    string `json:"path"`
				Content string `json:"content"` // base64
			} `json:"files"`
			SourceControlURL string `json:"sourceControlUrl"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
		return true
	}
	if len(req.Contents) == 0 {
		connectError(w, http.StatusBadRequest, "invalid_argument", "contents is required")
		return true
	}

	commits := make([]map[string]any, 0, len(req.Contents))
	for _, c := range req.Contents {
		if c.ModuleRef == nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "moduleRef is required")
			return true
		}
		owner, module := c.ModuleRef.Value.Name.Owner, c.ModuleRef.Value.Name.Module
		if owner == "" || module == "" {
			connectError(w, http.StatusBadRequest, "invalid_argument", "moduleRef.name is required")
			return true
		}
		if len(c.Files) == 0 {
			connectError(w, http.StatusBadRequest, "invalid_argument", "files is required")
			return true
		}
		files := make(map[string][]byte, len(c.Files))
		for _, f := range c.Files {
			if f.Path == "" {
				connectError(w, http.StatusBadRequest, "invalid_argument", "a file has no path")
				return true
			}
			raw, derr := base64.StdEncoding.DecodeString(f.Content)
			if derr != nil {
				connectError(w, http.StatusBadRequest, "invalid_argument",
					fmt.Sprintf("file %q content is not base64: %v", f.Path, derr))
				return true
			}
			files[f.Path] = raw
		}
		commit := shake256Hex([]byte(canonicalManifest(files)))
		if err := s.storePush(r.Context(), moduleID(owner, module), commit, commit, files, nil); err != nil {
			connectError(w, http.StatusInternalServerError, "internal", err.Error())
			return true
		}
		now := time.Now().UTC().Format(time.RFC3339)
		dig := shake256Hex([]byte(canonicalManifest(files)))
		rawDig, _ := hexDecode(dig)
		commits = append(commits, map[string]any{
			"id":         commit[:32],
			"createTime": now,
			"ownerId":    owner,
			"moduleId":   module,
			"digest":     map[string]any{"digestType": 1, "digest": base64.StdEncoding.EncodeToString(rawDig)},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"commits": commits})
	return true
}

// v1GetCommits makes a pushed commit resolvable, which is how a later read
// addresses specific content.
func (s *State) v1GetCommits(w http.ResponseWriter, r *http.Request, body []byte) bool {
	var req struct {
		CommitRefs []struct {
			Value struct {
				ID   string     `json:"id"`
				Full *v1ModuleRef `json:"full"`
			} `json:"value"`
		} `json:"commitRefs"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
		return true
	}
	commits := make([]map[string]any, 0, len(req.CommitRefs))
	for _, ref := range req.CommitRefs {
		id := ref.Value.ID
		owner, module := "", ""
		if ref.Value.Full != nil {
			owner, module = ref.Value.Full.Value.Name.Owner, ref.Value.Full.Value.Name.Module
		}
		if id == "" || owner == "" || module == "" {
			continue
		}
		if _, err := s.Registry.Meta.Get(r.Context(), "protobuf", moduleID(owner, module), id); err != nil {
			continue
		}
		commits = append(commits, map[string]any{
			"id": id, "ownerId": owner, "moduleId": module,
			"createTime": time.Now().UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"commits": commits})
	return true
}

// helpers kept local so this file does not depend on unexported names elsewhere.
func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var hi, lo byte
		hi = unhex(s[2*i])
		lo = unhex(s[2*i+1])
		if hi == 0xFF || lo == 0xFF {
			return nil, fmt.Errorf("invalid hex")
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0xFF
}

// sortedPaths is used by the archive builder and the JSON renderers.
func sortedPaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}
