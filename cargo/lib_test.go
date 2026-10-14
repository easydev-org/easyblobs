package cargo

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newState(t *testing.T) *State {
	t.Helper()
	dir := t.TempDir()
	meta, err := store.OpenSQLite(dir + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	blobs, err := store.NewFileBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	return &State{Registry: &easyblobkit.Registry{Blobs: blobs, Meta: meta,
		Upstreams: &easyblobkit.Upstreams{Defaults: map[string]string{}, Overrides: map[string]string{}, Proxy: map[string]string{}}}}
}

// publishBody builds cargo's publish framing: u32 json len, json, u32 crate
// len, crate bytes.
func publishBody(t *testing.T, name, version string, crate []byte) []byte {
	t.Helper()
	j, _ := json.Marshal(map[string]any{"name": name, "vers": version})
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(j)))
	buf.Write(j)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(crate)))
	buf.Write(crate)
	return buf.Bytes()
}

// TestPublishThenDownload verifies a published crate is served byte-identically.
func TestPublishThenDownload(t *testing.T) {
	s := newState(t)
	crate := []byte("crate-tarball-bytes")

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/artifacts/cargo/api/v1/crates/new", bytes.NewReader(publishBody(t, "anyhow", "1.0.0", crate))))
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artifacts/cargo/api/v1/crates/anyhow/1.0.0/download", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), crate) {
		t.Fatal("downloaded crate differs from the published bytes")
	}
}

// TestSparseIndexKeepsUpstreamFeatures is the regression test for the bug where a
// version cached via pull-through had its upstream deps/features replaced by an
// empty skeleton, so consumers (e.g. the `windows` crate's feature flags) broke.
func TestSparseIndexKeepsUpstreamFeatures(t *testing.T) {
	s := newState(t)
	// Simulate a pull-through cache hit for an upstream version.
	s.Registry.StoreVersion(context.Background(), easyblobkit.VersionInput{
		Format: "cargo", Repository: "windows", Version: "0.58.0",
		Filename: "windows-0.58.0.crate", Source: "pull", Data: []byte("x"),
	})
	up := `{"name":"windows","vers":"0.58.0","deps":[],"cksum":"UP","features":{"Graphics_Capture":["Graphics"]},"yanked":false,"links":null}`
	body := s.renderSparseIndex(context.Background(), "windows", []string{"0.58.0"}, up)
	var mv map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &mv); err != nil {
		t.Fatalf("bad line: %v (%s)", err, body)
	}
	feats, _ := mv["features"].(map[string]any)
	if _, ok := feats["Graphics_Capture"]; !ok {
		t.Fatalf("upstream features were dropped: %s", body)
	}
}
