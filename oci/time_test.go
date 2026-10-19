package oci

import (
	"testing"
	"time"
)

// TestManifestCreated checks the image creation time is read from the OCI
// annotation, then the inline config, and otherwise falls back to the default.
func TestManifestCreated(t *testing.T) {
	def := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Annotation wins.
	m := []byte(`{"annotations":{"org.opencontainers.image.created":"2024-05-06T07:08:09Z"}}`)
	if got := manifestCreated(m, def); !got.Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Errorf("annotation: got %v", got)
	}
	// Inline config.
	m = []byte(`{"config":{"created":"2023-02-03T04:05:06Z"}}`)
	if got := manifestCreated(m, def); !got.Equal(time.Date(2023, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Errorf("config: got %v", got)
	}
	// Neither: fall back.
	if got := manifestCreated([]byte(`{}`), def); !got.Equal(def) {
		t.Errorf("fallback: got %v", got)
	}
	// Bad JSON: fall back.
	if got := manifestCreated([]byte(`not json`), def); !got.Equal(def) {
		t.Errorf("bad json: got %v", got)
	}
}
