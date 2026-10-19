package npm

import (
	"context"
	"testing"
	"time"
)

// TestPublishSetsTimestamps checks a published version records both StoredAt
// and PublishedAt (the "package updated" time).
func TestPublishSetsTimestamps(t *testing.T) {
	s := newState(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Second)
	s.storeVersion(ctx, "left-pad", "1.3.0", tarball(t, "left-pad", "1.3.0"), map[string]any{"name": "left-pad", "version": "1.3.0"}, "push")
	after := time.Now().Add(time.Second)

	art, err := s.Registry.Meta.Get(ctx, "npm", "left-pad", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if art.StoredAt.Before(before) || art.StoredAt.After(after) {
		t.Errorf("StoredAt = %v, want within [%v,%v]", art.StoredAt, before, after)
	}
	if art.PublishedAt.Before(before) || art.PublishedAt.After(after) {
		t.Errorf("PublishedAt = %v, want within [%v,%v]", art.PublishedAt, before, after)
	}
}

// TestStoreVersionAtHonorsPublishTime checks an explicit publish time (from a
// packument's `time` map on a pull-through) is recorded verbatim.
func TestStoreVersionAtHonorsPublishTime(t *testing.T) {
	s := newState(t)
	ctx := context.Background()
	pub := time.Date(2022, 3, 4, 5, 6, 7, 0, time.UTC)
	s.storeVersionAt(ctx, "left-pad", "1.1.1", tarball(t, "left-pad", "1.1.1"), nil, "pull", pub)

	art, err := s.Registry.Meta.Get(ctx, "npm", "left-pad", "1.1.1")
	if err != nil {
		t.Fatal(err)
	}
	if !art.PublishedAt.Equal(pub) {
		t.Errorf("PublishedAt = %v, want %v", art.PublishedAt, pub)
	}
	if art.StoredAt.IsZero() {
		t.Errorf("StoredAt should be set")
	}
}
