package store

import (
	"context"
	"testing"
	"time"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// TestStoredPublishedRoundTrip checks StoredAt/PublishedAt survive a Put/Get,
// and that a zero time is stored as 0 (unknown), not a negative epoch.
func TestStoredPublishedRoundTrip(t *testing.T) {
	s, err := OpenSQLite(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	stored := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	published := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	art := easyblobkit.Artifact{
		Format: "npm", Repository: "left-pad", Version: "1.0.0", Digest: "d1",
		Source: "pull", StoredAt: stored, PublishedAt: published,
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "npm", "left-pad", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !got.StoredAt.Equal(stored) {
		t.Errorf("StoredAt = %v, want %v", got.StoredAt, stored)
	}
	if !got.PublishedAt.Equal(published) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, published)
	}

	// A zero time round-trips as the zero time (stored as 0).
	if err := s.Put(ctx, easyblobkit.Artifact{
		Format: "npm", Repository: "x", Version: "0", Digest: "d2",
	}); err != nil {
		t.Fatal(err)
	}
	got2, err := s.Get(ctx, "npm", "x", "0")
	if err != nil {
		t.Fatal(err)
	}
	if !got2.StoredAt.IsZero() || !got2.PublishedAt.IsZero() {
		t.Errorf("zero times should stay zero: %+v", got2)
	}
}
