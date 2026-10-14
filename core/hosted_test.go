package easyblobkit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// TestHostedStorageKeyUnchanged locks the invariant that the explicit
// "_hosted/<repo>/" addressing does NOT change the stored coordinates: a
// hosted row is still indexed under repository "<repo>/hosted/<repo>"
// (Namespace=<repo> via the middleware + repoKey="hosted/<repo>"), so rows
// written before this change keep resolving.
func TestHostedStorageKeyUnchanged(t *testing.T) {
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
	reg := &easyblobkit.Registry{Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta)}

	// Drive HostedUpload under the scope the middleware produces for
	// "/artifacts/rpm/_hosted/fedora41/...".
	sc := easyblobkit.RepoScope{Format: "rpm", Namespace: "fedora41", Explicit: true, Hosted: true}
	ctx := easyblobkit.WithRepoScope(context.Background(), sc)
	hs := easyblobkit.HostedStore{Registry: reg}
	if _, _, err := hs.HostedUpload(ctx, "rpm", "fedora41", "repodata/repomd.xml", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}

	// Stored under "<repo>/hosted/<repo>" = "fedora41/hosted/fedora41".
	if _, err := meta.Get(context.Background(), "rpm", "fedora41/hosted/fedora41", "repodata/repomd.xml"); err != nil {
		t.Fatalf("stored key wrong: %v", err)
	}
	// And the adapter's read under the same scope resolves it.
	if _, ok := hs.HostedFile(ctx, "rpm", "fedora41", "repodata/repomd.xml"); !ok {
		t.Fatal("hosted file not visible through the scoped read")
	}
}

// TestHostedGetMissIs404 verifies HostedHandler.Get no longer reports a
// "fall back to proxy" signal: a miss is an immediate 404.
func TestHostedGetMissIs404(t *testing.T) {
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
	reg := &easyblobkit.Registry{Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta)}
	h := &easyblobkit.HostedHandler{Format: "rpm", Store: easyblobkit.HostedStore{Registry: reg}}

	ctx := easyblobkit.WithRepoScope(context.Background(),
		easyblobkit.RepoScope{Format: "rpm", Namespace: "nope", Explicit: true, Hosted: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/artifacts/rpm/_hosted/nope/repomd.xml", nil).WithContext(ctx)
	h.Get(rec, req, "nope", "repomd.xml")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("miss status = %d, want 404", rec.Code)
	}
}
