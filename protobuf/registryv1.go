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

	// buf's Connect client sends application/proto - BINARY, not JSON. The
	// server log is explicit about it:
	//   req POST /buf.registry.module.v1.ModuleService/GetModules
	//       ct="application/proto"
	// so the binary form is decoded here; the JSON form is kept for hand-written
	// callers and for `curl`-style testing.
	binary := strings.HasPrefix(r.Header.Get("Content-Type"), "application/proto")

	switch svc + "/" + method {
	case "ModuleService/GetModules":
		return s.v1GetModules(w, r, body, binary)
	case "ModuleService/CreateModules":
		return s.v1CreateModules(w, body, r, binary)
	case "UploadService/Upload":
		return s.v1Upload(w, body, r, binary)
	case "CommitService/GetCommits":
		return s.v1GetCommits(w, r, body, binary)
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

// v1ModuleWire renders the same Module message in protobuf wire format.
//
// Field numbers from buf/registry/module/v1/module.proto:
//  1 id, 2 create_time, 3 update_time, 4 name, 5 owner_id,
//  6 visibility, 7 state, 8 description, 10 default_label_name
//
// create_time/update_time are google.protobuf.Timestamp (seconds = 1, nanos = 2);
// a lenient client only needs them present and parseable.
func v1ModuleWire(owner, module string) []byte {
	now := uint64(time.Now().Unix())
	var ts []byte
	ts = wireAppendVarintField(ts, 1, now) // seconds

	var b []byte
	b = wireAppendString(b, 1, owner+"-"+module) // id
	b = wireAppendMessage(b, 2, ts)              // create_time
	b = wireAppendMessage(b, 3, ts)              // update_time
	b = wireAppendString(b, 4, module)           // name
	b = wireAppendString(b, 5, owner)            // owner_id
	b = wireAppendVarintField(b, 6, 1)           // visibility = PUBLIC
	b = wireAppendVarintField(b, 7, 1)           // state = ACTIVE
	b = wireAppendString(b, 8, "published through easyblobs")
	b = wireAppendString(b, 10, "main")          // default_label_name
	return b
}

// v1CommitWire renders a Commit in wire format.
//  1 id, 2 create_time, 3 owner_id, 4 module_id, 5 digest
//  Digest { 1 digest_type (enum), 2 digest (bytes) }
func v1CommitWire(id, owner, module, manifestHex string) []byte {
	now := uint64(time.Now().Unix())
	var ts []byte
	ts = wireAppendVarintField(ts, 1, now)

	rawDigest, _ := hexDecode(manifestHex)
	var dig []byte
	dig = wireAppendVarintField(dig, 1, 1) // DIGEST_TYPE_SHAKE256
	dig = wireAppendBytes(dig, 2, rawDigest)

	var b []byte
	b = wireAppendString(b, 1, id)
	b = wireAppendMessage(b, 2, ts)
	b = wireAppendString(b, 3, owner)
	b = wireAppendString(b, 4, module)
	b = wireAppendMessage(b, 5, dig)
	return b
}

// v1GetModules answers "does this module exist?".
//
// A module exists when this registry holds at least one pushed commit for it.
// When it does not, the response must be EMPTY (not an error): that is what
// tells buf to create it.
func (s *State) v1GetModules(w http.ResponseWriter, r *http.Request, body []byte, binary bool) bool {
	// GetModulesRequest { repeated ModuleRef module_refs = 1; }
	// ModuleRef { oneof value { string id = 1; Name name = 2 } }
	// ModuleRef.Name { string owner = 1; string module = 2 }
	type ref struct{ owner, module string }
	var refs []ref

	if binary {
		fields, err := wireDecode(body)
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, raw := range allBytes(fields, 1) { // module_refs
			rf, err := wireDecode(raw)
			if err != nil {
				continue
			}
			nameRaw := firstBytes(rf, 2) // ModuleRef.name
			if nameRaw == nil {
				continue
			}
			nf, err := wireDecode(nameRaw)
			if err != nil {
				continue
			}
			refs = append(refs, ref{owner: firstString(nf, 1), module: firstString(nf, 2)})
		}
	} else {
		var req struct {
			ModuleRefs []v1ModuleRef `json:"moduleRefs"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, x := range req.ModuleRefs {
			refs = append(refs, ref{owner: x.Value.Name.Owner, module: x.Value.Name.Module})
		}
	}

	// GetModulesResponse { repeated Module modules = 1; }
	var out []byte
	mods := make([]map[string]any, 0, len(refs))
	for _, rf := range refs {
		if rf.owner == "" || rf.module == "" {
			continue
		}
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "protobuf", moduleID(rf.owner, rf.module))
		if len(vs) == 0 {
			continue
		}
		m := v1ModuleJSON(rf.owner, rf.module)
		mods = append(mods, m)
		if binary {
			out = wireAppendMessage(out, 1, v1ModuleWire(rf.owner, rf.module))
		}
	}
	if binary {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(out)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"modules": mods})
	return true
}

// v1CreateModules registers the module's name. Nothing else is needed: the
// files arrive through Upload.
func (s *State) v1CreateModules(w http.ResponseWriter, body []byte, r *http.Request, binary bool) bool {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "protobuf") {
		return true
	}
	// CreateModulesRequest { repeated CreateModulesRequest.Value values = 1; }
	// Value { ModuleRef.Name name = 1; visibility = 4 }
	type name struct{ owner, module string }
	var names []name

	if binary {
		fields, err := wireDecode(body)
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, raw := range allBytes(fields, 1) { // values
			vf, err := wireDecode(raw)
			if err != nil {
				continue
			}
			nameRaw := firstBytes(vf, 1) // name
			if nameRaw == nil {
				continue
			}
			nf, err := wireDecode(nameRaw)
			if err != nil {
				continue
			}
			names = append(names, name{owner: firstString(nf, 1), module: firstString(nf, 2)})
		}
	} else {
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
		for _, v := range req.Values {
			names = append(names, name{owner: v.Name.Owner, module: v.Name.Module})
		}
	}

	mods := make([]map[string]any, 0, len(names))
	var out []byte
	for _, n := range names {
		if n.owner == "" || n.module == "" {
			connectError(w, http.StatusBadRequest, "invalid_argument", "module name is required")
			return true
		}
		mods = append(mods, v1ModuleJSON(n.owner, n.module))
		if binary {
			out = wireAppendMessage(out, 1, v1ModuleWire(n.owner, n.module))
		}
	}
	if binary {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(out)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"modules": mods})
	return true
}

// v1Upload stores the module's files - this is what `buf push` actually sends.
func (s *State) v1Upload(w http.ResponseWriter, body []byte, r *http.Request, binary bool) bool {
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "protobuf") {
		return true
	}
	// UploadRequest { repeated Content contents = 1; }
	// Content { ModuleRef module_ref = 1; repeated File files = 2;
	//           repeated ScopedLabelRef scoped_label_refs = 3;
	//           string source_control_url = 4 }
	// File { string path = 1; bytes content = 2 }
	type upload struct {
		owner, module string
		files         map[string][]byte
	}
	var ups []upload

	if binary {
		fields, err := wireDecode(body)
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, raw := range allBytes(fields, 1) { // contents
			cf, err := wireDecode(raw)
			if err != nil {
				continue
			}
			var u upload
			u.files = map[string][]byte{}
			if refRaw := firstBytes(cf, 1); refRaw != nil { // module_ref
				if rf, err := wireDecode(refRaw); err == nil {
					if nameRaw := firstBytes(rf, 2); nameRaw != nil {
						if nf, err := wireDecode(nameRaw); err == nil {
							u.owner, u.module = firstString(nf, 1), firstString(nf, 2)
						}
					}
				}
			}
			for _, fRaw := range allBytes(cf, 2) { // files
				ff, err := wireDecode(fRaw)
				if err != nil {
					continue
				}
				p := firstString(ff, 1)
				c := firstBytes(ff, 2)
				if p != "" {
					u.files[p] = c
				}
			}
			ups = append(ups, u)
		}
	} else {
		var req struct {
			Contents []struct {
				ModuleRef *v1ModuleRef `json:"moduleRef"`
				Files     []struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				} `json:"files"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, c := range req.Contents {
			var u upload
			u.files = map[string][]byte{}
			if c.ModuleRef != nil {
				u.owner, u.module = c.ModuleRef.Value.Name.Owner, c.ModuleRef.Value.Name.Module
			}
			for _, f := range c.Files {
				raw, derr := base64.StdEncoding.DecodeString(f.Content)
				if derr != nil {
					connectError(w, http.StatusBadRequest, "invalid_argument",
						fmt.Sprintf("file %q content is not base64: %v", f.Path, derr))
					return true
				}
				if f.Path != "" {
					u.files[f.Path] = raw
				}
			}
			ups = append(ups, u)
		}
	}

	if len(ups) == 0 {
		connectError(w, http.StatusBadRequest, "invalid_argument", "contents is required")
		return true
	}

	commits := make([]map[string]any, 0, len(ups))
	var out []byte
	for _, u := range ups {
		if u.owner == "" || u.module == "" {
			connectError(w, http.StatusBadRequest, "invalid_argument", "moduleRef.name is required")
			return true
		}
		if len(u.files) == 0 {
			connectError(w, http.StatusBadRequest, "invalid_argument", "files is required")
			return true
		}
		commit := shake256Hex([]byte(canonicalManifest(u.files)))
		if err := s.storePush(r.Context(), moduleID(u.owner, u.module), commit, commit, u.files, nil); err != nil {
			connectError(w, http.StatusInternalServerError, "internal", err.Error())
			return true
		}
		commits = append(commits, map[string]any{
			"id":         commit[:32],
			"createTime": time.Now().UTC().Format(time.RFC3339),
			"ownerId":    u.owner,
			"moduleId":   u.module,
			"digest": map[string]any{
				"digestType": 1,
				"digest":     base64.StdEncoding.EncodeToString(hexDecodeOrNil(commit)),
			},
		})
		if binary {
			out = wireAppendMessage(out, 1, v1CommitWire(commit[:32], u.owner, u.module, commit))
		}
	}

	if binary {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(out)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"commits": commits})
	return true
}

// hexDecodeOrNil decodes hex, returning nil on error (used for a best-effort
// digest in the JSON view).
func hexDecodeOrNil(s string) []byte {
	b, err := hexDecode(s)
	if err != nil {
		return nil
	}
	return b
}

// v1GetCommits makes a pushed commit resolvable, which is how a later read
// addresses specific content.
func (s *State) v1GetCommits(w http.ResponseWriter, r *http.Request, body []byte, binary bool) bool {
	// GetCommitsRequest { repeated CommitRef commit_refs = 1; }
	// CommitRef { oneof value { string id = 1; Full full = 2 } }
	// CommitRef.Full { ModuleRef module_ref = 1; string commit = 2 }
	type cref struct{ id, owner, module string }
	var refs []cref

	if binary {
		fields, err := wireDecode(body)
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, raw := range allBytes(fields, 1) { // commit_refs
			cf, err := wireDecode(raw)
			if err != nil {
				continue
			}
			inner, err := wireDecode(firstBytes(cf, 1)) // value oneof: id or full
			if err != nil {
				continue
			}
			c := cref{id: firstString(inner, 1)} // id
			if fullRaw := firstBytes(inner, 2); fullRaw != nil {
				ff, err := wireDecode(fullRaw)
				if err == nil {
					c.module = firstString(ff, 2) // commit
					if refRaw := firstBytes(ff, 1); refRaw != nil {
						if rf, err := wireDecode(refRaw); err == nil {
							if nameRaw := firstBytes(rf, 2); nameRaw != nil {
								if nf, err := wireDecode(nameRaw); err == nil {
									c.owner, c.module = firstString(nf, 1), firstString(nf, 2)
								}
							}
						}
					}
				}
			}
			refs = append(refs, c)
		}
	} else {
		var req struct {
			CommitRefs []struct {
				Value struct {
					ID   string      `json:"id"`
					Full *v1ModuleRef `json:"full"`
				} `json:"value"`
			} `json:"commitRefs"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
		for _, x := range req.CommitRefs {
			c := cref{id: x.Value.ID}
			if x.Value.Full != nil {
				c.owner, c.module = x.Value.Full.Value.Name.Owner, x.Value.Full.Value.Name.Module
			}
			refs = append(refs, c)
		}
	}

	commits := make([]map[string]any, 0, len(refs))
	var out []byte
	for _, c := range refs {
		if c.id == "" || c.owner == "" || c.module == "" {
			continue
		}
		if _, err := s.Registry.Meta.Get(r.Context(), "protobuf", moduleID(c.owner, c.module), c.id); err != nil {
			continue
		}
		commits = append(commits, map[string]any{
			"id": c.id, "ownerId": c.owner, "moduleId": c.module,
			"createTime": time.Now().UTC().Format(time.RFC3339),
		})
		if binary {
			out = wireAppendMessage(out, 1, v1CommitWire(c.id, c.owner, c.module, c.id))
		}
	}
	if binary {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(out)
		return true
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
