package easyblobkit_test

import (
	"context"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// TestDeleteVersionFileRemovesOnlyNamedFile locks the targeted delete: removing
// one filename leaves the version's other descriptors intact, and only deletes
// a blob that nothing else references.
func TestDeleteVersionFileRemovesOnlyNamedFile(t *testing.T) {
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
	reg := &easyblobkit.Registry{Blobs: blobs, Meta: meta}
	ctx := context.Background()

	// golden-macos/15 holds two files sharing one version row.
	ios, _, err := blobs.Put(ctx, strings.NewReader("ios-bytes"), "")
	if err != nil {
		t.Fatal(err)
	}
	xcode, _, err := blobs.Put(ctx, strings.NewReader("xcode-bytes"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := meta.Put(ctx, easyblobkit.Artifact{Format: "generic", Repository: "golden-macos", Version: "15",
		Blobs: []easyblobkit.Descriptor{
			{Name: "data-ios.qcow2", Digest: ios.Digest},
			{Name: "data-xcode.qcow2", Digest: xcode.Digest},
		}}); err != nil {
		t.Fatal(err)
	}

	deleted, err := reg.DeleteVersionFile(ctx, "generic", "golden-macos", "15", "data-ios.qcow2")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("expected the ios blob to be deleted")
	}
	got, _ := meta.Get(ctx, "generic", "golden-macos", "15")
	if len(got.Blobs) != 1 || got.Blobs[0].Name != "data-xcode.qcow2" {
		t.Fatalf("after delete, blobs = %+v (xcode must survive)", got.Blobs)
	}
	if sz, _ := blobs.Stat(ctx, xcode.Digest); sz == nil {
		t.Error("xcode blob must survive")
	}
	if sz, _ := blobs.Stat(ctx, ios.Digest); sz != nil {
		t.Error("ios blob must be gone")
	}
}
