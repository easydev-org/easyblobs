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

	// A row written with zero times gets a StoredAt backstop (now) and a
	// PublishedAt falling back to StoredAt — never a negative epoch.
	if err := s.Put(ctx, easyblobkit.Artifact{
		Format: "npm", Repository: "x", Version: "0", Digest: "d2",
	}); err != nil {
		t.Fatal(err)
	}
	got2, err := s.Get(ctx, "npm", "x", "0")
	if err != nil {
		t.Fatal(err)
	}
	if got2.StoredAt.IsZero() {
		t.Errorf("StoredAt backstop missing: %+v", got2)
	}
	if !got2.PublishedAt.Equal(got2.StoredAt) {
		t.Errorf("PublishedAt should fall back to StoredAt: %+v", got2)
	}
}

// TestStoredAtKeptOnRewrite checks a re-write (same format/repo/version) keeps
// the ORIGINAL StoredAt (first seen), not the rewrite time.
func TestStoredAtKeptOnRewrite(t *testing.T) {
	s, err := OpenSQLite(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	first := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Put(ctx, easyblobkit.Artifact{
		Format: "maven", Repository: "a", Version: "1", Digest: "d", StoredAt: first,
	}); err != nil {
		t.Fatal(err)
	}
	// A second Put (e.g. an extra file on the same version) with no StoredAt
	// must keep the original.
	if err := s.Put(ctx, easyblobkit.Artifact{
		Format: "maven", Repository: "a", Version: "1", Digest: "d2",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "maven", "a", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.StoredAt.Equal(first) {
		t.Errorf("StoredAt reset on rewrite: got %v want %v", got.StoredAt, first)
	}
}
