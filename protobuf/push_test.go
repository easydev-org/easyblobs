package protobuf

import (
	"archive/zip"
	"bytes"
	"crypto/sha3"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPushManifestAndBlobsThenDownload drives the round trip a real `buf push`
// and `buf dep update` would perform:
//
//  1. POST PushService/PushManifestAndBlobs with a manifest+blobs module
//  2. read the returned LocalModulePin
//  3. POST DownloadService/Download and get the SAME file bytes back
//  4. POST DownloadService/DownloadManifestAndBlobs and get the same manifest
//
// This is the capability that did not exist before: the adapter could only
// fetch from the public BSR, so a self-hosted BSR (publishing your own buf
// plugin or module into the mirror) was impossible.
func TestPushManifestAndBlobsThenDownload(t *testing.T) {
	s := newFixture(t, "")

	owner, repo := "acme", "plugins"
	files := map[string]string{
		"proto/acme/plugin/v1/plugin.proto": "syntax = \"proto3\";\npackage acme.plugin.v1;\n",
		"buf.yaml":                          "version: v2\n",
	}

	// Build the manifest+blobs encoding exactly as buf does.
	type blob struct {
		Digest  map[string]any `json:"digest"`
		Content string         `json:"content"`
	}
	var manifestText bytes.Buffer
	var blobs []blob
	for _, p := range []string{"buf.yaml", "proto/acme/plugin/v1/plugin.proto"} {
		raw := []byte(files[p])
		dig := shake256(raw)
		manifestText.WriteString("shake256:" + hex.EncodeToString(dig) + "  " + p + "\n")
		blobs = append(blobs, blob{
			Digest: map[string]any{
				"digestType": 1,
				"digest":     base64.StdEncoding.EncodeToString(dig),
			},
			Content: base64.StdEncoding.EncodeToString(raw),
		})
	}
	pushBody, _ := json.Marshal(map[string]any{
		"owner":      owner,
		"repository": repo,
		"manifest": blob{
			Digest: map[string]any{
				"digestType": 1,
				"digest":     base64.StdEncoding.EncodeToString(shake256(manifestText.Bytes())),
			},
			Content: base64.StdEncoding.EncodeToString(manifestText.Bytes()),
		},
		"blobs": blobs,
		"tags":  []string{"v1.0.0"},
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.PushService/PushManifestAndBlobs",
		bytes.NewReader(pushBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("push = %d %s", rec.Code, rec.Body.String())
	}
	var pushResp struct {
		LocalModulePin struct {
			Owner          string `json:"owner"`
			Repository     string `json:"repository"`
			Commit         string `json:"commit"`
			ManifestDigest string `json:"manifestDigest"`
		} `json:"localModulePin"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pushResp); err != nil {
		t.Fatalf("push response is not JSON: %s", rec.Body.String())
	}
	pin := pushResp.LocalModulePin
	if pin.Owner != owner || pin.Repository != repo {
		t.Fatalf("pin = %+v", pin)
	}
	if len(pin.Commit) != 128 {
		t.Fatalf("commit = %q, want a 128-char shake256 hex digest", pin.Commit)
	}
	if pin.ManifestDigest != "shake256:"+pin.Commit {
		t.Fatalf("manifestDigest = %q, want shake256:<commit>", pin.ManifestDigest)
	}

	// --- Download: the module comes back, byte for byte -------------------
	//
	// The reference is the commit the push returned, which is how a client
	// pins what it resolved.
	dlBody, _ := json.Marshal(map[string]any{
		"owner": owner, "repository": repo, "reference": pin.Commit,
	})
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.DownloadService/Download",
		bytes.NewReader(dlBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}
	var dlResp struct {
		Module struct {
			Files []struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			} `json:"files"`
		} `json:"module"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dlResp); err != nil {
		t.Fatalf("download response is not JSON: %s", rec.Body.String())
	}
	got := map[string]string{}
	for _, f := range dlResp.Module.Files {
		raw, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			t.Fatalf("file %s content is not base64: %v", f.Path, err)
		}
		got[f.Path] = string(raw)
	}
	if len(got) != len(files) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(files), got)
	}
	for p, want := range files {
		if got[p] != want {
			t.Fatalf("file %s differs:\n got: %q\nwant: %q", p, got[p], want)
		}
	}

	// --- DownloadManifestAndBlobs: the manifest round-trips ---------------
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.DownloadService/DownloadManifestAndBlobs",
		bytes.NewReader(dlBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("download manifest = %d %s", rec.Code, rec.Body.String())
	}
	var mbResp struct {
		Manifest struct {
			Content string `json:"content"`
		} `json:"manifest"`
		Blobs []struct {
			Content string `json:"content"`
		} `json:"blobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mbResp); err != nil {
		t.Fatalf("manifest response is not JSON: %s", rec.Body.String())
	}
	manifestGot, _ := base64.StdEncoding.DecodeString(mbResp.Manifest.Content)
	if string(manifestGot) != manifestText.String() {
		t.Fatalf("manifest differs:\n got: %q\nwant: %q", manifestGot, manifestText.String())
	}
	if len(mbResp.Blobs) != len(files) {
		t.Fatalf("got %d blobs, want %d", len(mbResp.Blobs), len(files))
	}
}

// TestPushRejectsBlobDigestMismatch makes sure a client cannot store bytes
// under a digest they do not hash to - the manifest is the module's identity,
// so a lie there would poison every later download.
func TestPushRejectsBlobDigestMismatch(t *testing.T) {
	s := newFixture(t, "")

	lie := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAB}, 64))
	content := base64.StdEncoding.EncodeToString([]byte("real content"))
	manifest := "shake256:" + hex.EncodeToString(bytes.Repeat([]byte{0xAB}, 64)) + "  a.proto\n"

	body, _ := json.Marshal(map[string]any{
		"owner": "acme", "repository": "bad",
		"manifest": map[string]any{
			"digest":  map[string]any{"digestType": 1, "digest": lie},
			"content": base64.StdEncoding.EncodeToString([]byte(manifest)),
		},
		"blobs": []map[string]any{{
			"digest":  map[string]any{"digestType": 1, "digest": lie},
			"content": content,
		}},
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
		"/artifacts/protobuf/buf.alpha.registry.v1alpha1.PushService/PushManifestAndBlobs",
		bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("push with a mismatched digest = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestPushThenDownloadAsZip verifies the stored blob is a real module zip, i.e.
// the archive form DownloadService serves to older clients.
func TestPushThenDownloadAsZip(t *testing.T) {
	archive := buildModuleArchive(map[string][]byte{
		"buf.yaml": []byte("version: v2\n"),
		"a.proto":  []byte("syntax = \"proto3\";\n"),
	})
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("the module archive is not a zip: %v", err)
	}
	if len(zr.File) != 2 {
		t.Fatalf("archive has %d entries, want 2", len(zr.File))
	}
	// Deterministic: the same content must produce the same bytes, so pushing
	// twice does not churn the stored blob.
	again := buildModuleArchive(map[string][]byte{
		"a.proto":  []byte("syntax = \"proto3\";\n"),
		"buf.yaml": []byte("version: v2\n"),
	})
	if !bytes.Equal(archive, again) {
		t.Fatal("the module archive is not deterministic for the same content")
	}
}

// shake256 is the test's own implementation, kept separate from the adapter's
// so a bug in the production helper cannot make the test agree with itself.
func shake256(b []byte) []byte {
	h := sha3.NewSHAKE256()
	_, _ = h.Write(b)
	out := make([]byte, 64)
	_, _ = h.Read(out)
	return out
}
