package protobuf

// Local BSR publishing.
//
// Until now this adapter was pure passthrough: it forwarded Connect calls to
// the public BSR and cached the responses. That means you could CONSUME buf
// modules through the mirror but never publish one to it - so a self-hosted
// BSR was impossible and "publish my own buf plugin" could not work.
//
// What a real `buf push` does (from buf's own proto definitions,
// buf/alpha/registry/v1alpha1/push.proto):
//
//	service PushService {
//	  rpc Push(PushRequest) returns (PushResponse);
//	  rpc PushManifestAndBlobs(PushManifestAndBlobsRequest) returns (...);
//	}
//
// PushManifestAndBlobs is what current clients use. Its request carries the
// module as a MANIFEST plus BLOBS:
//
//	message PushManifestAndBlobsRequest {
//	  string owner = 1;
//	  string repository = 2;
//	  Blob manifest = 3;          // text: "<digest_type>:<hex><SP><SP><path><LF>" per file
//	  repeated Blob blobs = 4;    // one per distinct digest
//	  repeated string tags = 5;
//	  string draft_name = 6;
//	}
//	message Blob { Digest digest = 1; bytes content = 2; }
//	message Digest { DigestType digest_type = 1; bytes digest = 2; }
//
// The digest is SHAKE256 over the file content, 64 bytes, lowercase hex, and
// the commit id is SHAKE256 over the manifest blob's content. Both are computed
// here, so a module pushed through the mirror is byte-identical to what the
// public BSR would have stored.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// bsrPushService is the fully-qualified service name a buf client calls.
//
// TWO API generations exist and the CLI we test against uses the NEW one:
//
//	buf.alpha.registry.v1alpha1  the original (alpha) API
//	buf.registry.module.v1       the STABLE v1 API, used by buf >= 1.4x
//
// The alpha service names are kept so older clients still work, and the v1
// names are handled too. Which one buf picks is visible in its own error:
//
//	Failure: unimplemented: 404 Not Found for
//	  https://<host>/buf.registry.module.v1.ModuleService/GetModules
const (
	bsrPushService      = "buf.alpha.registry.v1alpha1.PushService"
	bsrPushServiceV1    = "buf.registry.module.v1.ModuleService"
	bsrCommitServiceV1  = "buf.registry.module.v1.CommitService"
	bsrDownloadV1       = "buf.registry.module.v1.DownloadService"
)

// shake256Hex returns the lowercase hex SHAKE256 digest (64 bytes) of b.
func shake256Hex(b []byte) string {
	h := sha3.NewSHAKE256()
	_, _ = h.Write(b)
	out := make([]byte, 64)
	_, _ = h.Read(out)
	return hex.EncodeToString(out)
}

// connectJSON is the wire form of a protobuf message in the Connect protocol's
// JSON encoding: identical to proto3 JSON, i.e. lowerCamelCase field names and
// base64 (standard, padded) for `bytes`.
type connectBlob struct {
	Digest  *connectDigest `json:"digest,omitempty"`
	Content string         `json:"content,omitempty"` // base64
}

type connectDigest struct {
	DigestType json.RawMessage `json:"digestType,omitempty"` // 1 or "DIGEST_TYPE_SHAKE256"
	Digest     string          `json:"digest,omitempty"`     // base64
}

// pushManifestAndBlobs decodes the request. Field numbers are not available in
// JSON, so the names come straight from the .proto above.
type pushManifestAndBlobsReq struct {
	Owner      string        `json:"owner"`
	Repository string        `json:"repository"`
	Manifest   *connectBlob  `json:"manifest"`
	Blobs      []connectBlob `json:"blobs"`
	Tags       []string      `json:"tags"`
	DraftName  string        `json:"draftName"`
}

// pushReq is the older Push(PushRequest) shape: the whole module inline.
type pushReq struct {
	Owner      string        `json:"owner"`
	Repository string        `json:"repository"`
	Module     *inlineModule `json:"module"`
	Tags       []string      `json:"tags"`
	DraftName  string        `json:"draftName"`
}

type inlineModule struct {
	Files []inlineFile `json:"files"`
}

type inlineFile struct {
	Path    string `json:"path"`
	Content string `json:"content"` // base64
}

// isPushCall reports whether path addresses the push service.
func isPushCall(path string) (method string, ok bool) {
	// Accept both the fully-qualified and the short form: a client configured
	// with this host as its BSR calls "/buf.alpha.registry.v1alpha1.PushService/Push".
	for _, p := range []string{bsrPushService + "/", "PushService/"} {
		if strings.HasPrefix(path, p) {
			return strings.TrimPrefix(path, p), true
		}
	}
	return "", false
}

// servePush handles a local publish. It returns true when it answered.
func (s *State) servePush(w http.ResponseWriter, r *http.Request, path string) bool {
	method, ok := isPushCall(path)
	if !ok {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	// A push mutates state, so it must never be anonymous unless auth is off.
	if !easyblobkit.AuthorizeWriteScoped(w, r, s.Auth, s.Registry, "protobuf") {
		return true
	}
	easyblobkit.LimitBody(w, r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		easyblobkit.WriteReadErr(w, err)
		return true
	}

	var owner, repo, commit, manifestDigest string
	var files map[string][]byte
	var tags []string

	switch method {
	case "PushManifestAndBlobs":
		owner, repo, commit, manifestDigest, files, tags, err = decodeManifestAndBlobs(body)
	case "Push":
		owner, repo, commit, manifestDigest, files, tags, err = decodeInlineModule(body)
	default:
		// Not a push method we own: let the passthrough handle it (CreateModule,
		// DeleteModule, ... live on other services, but be lenient here).
		return false
	}
	if err != nil {
		connectError(w, http.StatusBadRequest, "invalid_argument", err.Error())
		return true
	}
	if owner == "" || repo == "" {
		connectError(w, http.StatusBadRequest, "invalid_argument", "owner and repository are required")
		return true
	}
	if len(files) == 0 {
		connectError(w, http.StatusBadRequest, "invalid_argument", "the module has no files")
		return true
	}

	// Store the module indexed by owner/repository and the commit digest, which
	// is what a later read (DownloadService/Download, GetModules) addresses.
	id := owner + "/" + repo
	if err := s.storePush(r.Context(), id, commit, manifestDigest, files, tags); err != nil {
		connectError(w, http.StatusInternalServerError, "internal", err.Error())
		return true
	}

	// The response is LocalModulePin, exactly as the public BSR returns it.
	resp := map[string]any{
		"localModulePin": map[string]any{
			"owner":          owner,
			"repository":     repo,
			"commit":         commit,
			"manifestDigest": "shake256:" + manifestDigest,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
	return true
}

// decodeManifestAndBlobs implements the manifest+blobs encoding.
//
// The manifest blob's CONTENT is a text file, one line per module file:
//
//	<digest_type>:<hex digest>[SP][SP]<path>[LF]
//
// The blobs are the file contents, one per distinct digest (files with the same
// content share one blob). Reconstructing the module means joining the two.
func decodeManifestAndBlobs(body []byte) (owner, repo, commit, manifestDigest string, files map[string][]byte, tags []string, err error) {
	var req pushManifestAndBlobsReq
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", "", "", nil, nil, fmt.Errorf("decode request: %w", err)
	}
	if req.Manifest == nil {
		return "", "", "", "", nil, nil, errors.New("manifest is required")
	}
	manifestBytes, derr := base64.StdEncoding.DecodeString(req.Manifest.Content)
	if derr != nil {
		return "", "", "", "", nil, nil, fmt.Errorf("manifest content is not base64: %w", derr)
	}

	// Index the blobs by their hex digest so the manifest can reference them.
	byDigest := make(map[string][]byte, len(req.Blobs))
	for _, b := range req.Blobs {
		if b.Digest == nil {
			return "", "", "", "", nil, nil, errors.New("a blob has no digest")
		}
		raw, derr := base64.StdEncoding.DecodeString(b.Digest.Digest)
		if derr != nil {
			return "", "", "", "", nil, nil, fmt.Errorf("blob digest is not base64: %w", derr)
		}
		content, cerr := base64.StdEncoding.DecodeString(b.Content)
		if cerr != nil {
			return "", "", "", "", nil, nil, fmt.Errorf("blob content is not base64: %w", cerr)
		}
		byDigest[hex.EncodeToString(raw)] = content
	}

	files = make(map[string][]byte, len(byDigest))
	for _, line := range strings.Split(string(manifestBytes), "\n") {
		if line == "" {
			continue
		}
		// "<digest_type>:<digest>  <path>": two spaces separate digest and path.
		sep := strings.Index(line, "  ")
		if sep < 0 {
			return "", "", "", "", nil, nil, fmt.Errorf("malformed manifest line: %q", line)
		}
		digestPart, pathPart := line[:sep], line[sep+2:]
		colon := strings.Index(digestPart, ":")
		if colon < 0 {
			return "", "", "", "", nil, nil, fmt.Errorf("malformed digest in manifest: %q", digestPart)
		}
		algo, digHex := digestPart[:colon], digestPart[colon+1:]
		if algo != "shake256" {
			return "", "", "", "", nil, nil, fmt.Errorf("unsupported digest type %q", algo)
		}
		content, ok := byDigest[digHex]
		if !ok {
			return "", "", "", "", nil, nil, fmt.Errorf("manifest references a digest that was not uploaded: %s", digHex)
		}
		// Verify the content really hashes to the digest it claims: a client
		// must not be able to store bytes under a digest they do not match.
		if got := shake256Hex(content); got != digHex {
			return "", "", "", "", nil, nil, fmt.Errorf("blob %s does not match its digest %s", got, digHex)
		}
		files[pathPart] = content
	}

	// The commit is the digest of the manifest blob itself.
	commit = shake256Hex(manifestBytes)
	manifestDigest = commit
	return req.Owner, req.Repository, commit, manifestDigest, files, req.Tags, nil
}

// decodeInlineModule implements the older Push(PushRequest), where the module's
// files travel inline. Both are supported so older buf versions work.
func decodeInlineModule(body []byte) (owner, repo, commit, manifestDigest string, files map[string][]byte, tags []string, err error) {
	var req pushReq
	if err := json.Unmarshal(body, &req); err != nil {
		return "", "", "", "", nil, nil, fmt.Errorf("decode request: %w", err)
	}
	if req.Module == nil || len(req.Module.Files) == 0 {
		return "", "", "", "", nil, nil, errors.New("module.files is required")
	}
	files = make(map[string][]byte, len(req.Module.Files))
	for _, f := range req.Module.Files {
		if f.Path == "" {
			return "", "", "", "", nil, nil, errors.New("a module file has no path")
		}
		content, cerr := base64.StdEncoding.DecodeString(f.Content)
		if cerr != nil {
			return "", "", "", "", nil, nil, fmt.Errorf("file %q content is not base64: %w", f.Path, cerr)
		}
		files[f.Path] = content
	}
	// Synthesise the canonical manifest so both encodings produce the SAME
	// commit for the same module: that is what makes a push idempotent.
	manifestText := canonicalManifest(files)
	commit = shake256Hex([]byte(manifestText))
	return req.Owner, req.Repository, commit, commit, files, req.Tags, nil
}

// canonicalManifest renders the manifest text: one line per file, sorted by
// path, "<algo>:<hex>  <path>\n". Sorted output is what makes the commit digest
// stable regardless of the order the client sent files in.
func canonicalManifest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("shake256:")
		b.WriteString(shake256Hex(files[p]))
		b.WriteString("  ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}

// storePush persists the module so a subsequent read can reconstruct it.
//
// One descriptor per pushed commit carries the module archive; the tags are
// stored as their own versions so that resolving a tag (e.g. "v1") works the
// way a client expects.
func (s *State) storePush(ctx context.Context, id, commit, manifestDigest string,
	files map[string][]byte, tags []string) error {

	archive := buildModuleArchive(files)
	s.Registry.StoreVersion(ctx, easyblobkit.VersionInput{
		Format:     "protobuf",
		Repository: id,
		Version:    commit,
		Filename:   id + "-" + commit + ".zip",
		Source:     "push",
		Data:       archive,
	})
	for _, t := range tags {
		if t == "" {
			continue
		}
		s.Registry.StoreVersion(ctx, easyblobkit.VersionInput{
			Format:     "protobuf",
			Repository: id,
			Version:    t,
			Filename:   id + "-" + t + ".zip",
			Source:     "push",
			Data:       archive,
		})
	}
	return nil
}

// buildModuleArchive packs the module files into a deterministic zip, which is
// the shape DownloadService/Download serves.
func buildModuleArchive(files map[string][]byte) []byte {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A fixed timestamp keeps the archive byte-identical across pushes of the
	// same content, so the stored blob does not churn.
	epoch := time.Unix(0, 0).UTC()
	for _, p := range paths {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Deflate, Modified: epoch})
		if err != nil {
			continue
		}
		_, _ = fw.Write(files[p])
	}
	_ = zw.Close()
	return buf.Bytes()
}

// connectError writes an error in the Connect protocol's JSON shape, so a real
// buf client reports it instead of failing to parse the response.
func connectError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": msg})
}

// ---------------------------------------------------------------------------
// Reading a locally pushed module back.
//
// A client that resolved a module through this registry must be able to
// download it WITHOUT the upstream, otherwise pushing is pointless. buf reads
// through DownloadService, whose request is just owner/repository/reference:
//
//	message DownloadRequest { string owner = 1; string repository = 2;
//	                          string reference = 3; }
//	message DownloadResponse { Module module = 1; }
//	message DownloadManifestAndBlobsResponse { Blob manifest = 1;
//	                                           repeated Blob blobs = 2; }
// ---------------------------------------------------------------------------

type downloadReq struct {
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	Reference  string `json:"reference"`
}

// isDownloadCall reports whether path addresses the download service.
func isDownloadCall(path string) (method string, ok bool) {
	for _, p := range []string{
		"buf.alpha.registry.v1alpha1.DownloadService/",
		"DownloadService/",
	} {
		if strings.HasPrefix(path, p) {
			return strings.TrimPrefix(path, p), true
		}
	}
	return "", false
}

// serveLocalRead answers a download from the local store when the module was
// pushed here. It returns true when it answered.
func (s *State) serveLocalRead(w http.ResponseWriter, r *http.Request, path string) bool {
	method, ok := isDownloadCall(path)
	if !ok {
		return false
	}
	if method != "Download" && method != "DownloadManifestAndBlobs" {
		return false
	}

	var req downloadReq
	if r.Method == http.MethodGet {
		// The BSR also answers Download as a GET with query parameters (that is
		// how this mount already served it), so accept both shapes.
		req.Owner = r.URL.Query().Get("owner")
		req.Repository = r.URL.Query().Get("repository")
		req.Reference = r.URL.Query().Get("reference")
	} else {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBSRBody))
		if err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", err.Error())
			return true
		}
		if err := json.Unmarshal(body, &req); err != nil {
			connectError(w, http.StatusBadRequest, "invalid_argument", "decode request: "+err.Error())
			return true
		}
	}
	if req.Owner == "" || req.Repository == "" {
		// Not enough information to look anything up: let the upstream try.
		return false
	}

	id := req.Owner + "/" + req.Repository
	files, commit, found := s.loadPushedModule(r.Context(), id, req.Reference)
	if !found {
		// Not something we host: fall through to the upstream passthrough.
		return false
	}

	if method == "DownloadManifestAndBlobs" {
		// Rebuild the manifest + blobs encoding from the stored files.
		manifestText := canonicalManifest(files)
		blobs := make([]connectBlob, 0, len(files))
		seen := make(map[string]bool, len(files))
		paths := make([]string, 0, len(files))
		for p := range files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			dig := shake256Hex(files[p])
			if seen[dig] {
				continue
			}
			seen[dig] = true
			raw, _ := hex.DecodeString(dig)
			blobs = append(blobs, connectBlob{
				Digest:  &connectDigest{DigestType: json.RawMessage("1"), Digest: base64.StdEncoding.EncodeToString(raw)},
				Content: base64.StdEncoding.EncodeToString(files[p]),
			})
		}
		rawManifest, _ := hex.DecodeString(shake256Hex([]byte(manifestText)))
		resp := map[string]any{
			"manifest": map[string]any{
				"digest": map[string]any{
					"digestType": 1,
					"digest":     base64.StdEncoding.EncodeToString(rawManifest),
				},
				"content": base64.StdEncoding.EncodeToString([]byte(manifestText)),
			},
			"blobs": blobs,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return true
	}

	// DownloadResponse{ module: Module{ files: [{path, content}] } }
	filesOut := make([]map[string]any, 0, len(files))
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		filesOut = append(filesOut, map[string]any{
			"path":    p,
			"content": base64.StdEncoding.EncodeToString(files[p]),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"module": map[string]any{
			"files":  filesOut,
			"commit": commit,
		},
	})
	return true
}

// loadPushedModule reads a locally pushed module back out of the store.
//
// The archive stored by storePush is the deterministic zip built from the
// module's files, so unpacking it reconstructs the module exactly.
func (s *State) loadPushedModule(ctx context.Context, id, reference string) (map[string][]byte, string, bool) {
	versions := []string{}
	if reference != "" {
		versions = append(versions, reference)
	}

	// With no reference (a client that trusts the default branch), try every
	// version this repository has. Newest-first is not available here, so pick
	// the first that loads; a repository normally holds one pushed commit.
	if len(versions) == 0 {
		vs, err := s.Registry.Meta.ListVersions(ctx, "protobuf", id)
		if err != nil || len(vs) == 0 {
			return nil, "", false
		}
		versions = vs
	}

	for _, v := range versions {
		art, err := s.Registry.Meta.Get(ctx, "protobuf", id, v)
		if err != nil || len(art.Blobs) == 0 {
			continue
		}
		rd, err := s.Registry.Blobs.Open(ctx, art.Blobs[0].Digest)
		if err != nil || rd == nil {
			continue
		}
		data, _ := io.ReadAll(rd)
		_ = rd.Close()
		files, err := unzipModule(data)
		if err != nil || len(files) == 0 {
			continue
		}
		return files, v, true
	}
	return nil, "", false
}

// unzipModule unpacks the module archive stored by storePush.
func unzipModule(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 32<<20))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		files[f.Name] = b
	}
	return files, nil
}
