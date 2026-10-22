package protobuf

import (
	"crypto/sha3"
	"encoding/hex"
	"testing"
)

// TestSHAKE256Available pins the one crypto primitive a BSR push needs: the
// manifest and blob digests are SHAKE256 with a 64-byte output, lowercase hex.
// Go's stdlib sha3.NewSHAKE256 provides it, so no third-party dependency is
// required.
func TestSHAKE256Available(t *testing.T) {
	h := sha3.NewSHAKE256()
	_, _ = h.Write([]byte("hello"))
	out := make([]byte, 64)
	if _, err := h.Read(out); err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(out)
	if len(got) != 128 {
		t.Fatalf("digest hex length = %d, want 128", len(got))
	}
	t.Logf("shake256(\"hello\") = %s", got[:32])
}
