package easyblobkit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestTransientUpstream locks the classification adapters rely on to return
// 503+Retry-After (retryable) instead of a misleading 404 (definitive).
func TestTransientUpstream(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"502", &UpstreamStatusError{Path: "/x", Status: 502}, true},
		{"503", &UpstreamStatusError{Path: "/x", Status: 503}, true},
		{"504", &UpstreamStatusError{Path: "/x", Status: 504}, true},
		{"429", &UpstreamStatusError{Path: "/x", Status: 429}, true},
		{"404", &UpstreamStatusError{Path: "/x", Status: 404}, false},
		{"403", &UpstreamStatusError{Path: "/x", Status: 403}, false},
		{"transport", errors.New("dial tcp: connection refused"), true},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
	}
	for _, c := range cases {
		if got := TransientUpstream(c.err); got != c.want {
			t.Errorf("%s: TransientUpstream = %v want %v", c.name, got, c.want)
		}
	}
}

// TestJitterStaysNearBase keeps the retry wait within ±25% of the base so the
// budget stays bounded (a cold-cache miss must not stall for long).
func TestJitterStaysNearBase(t *testing.T) {
	const base = 4 * time.Second
	for i := 0; i < 200; i++ {
		d := jitter(base)
		if d < base*3/4 || d > base*5/4 {
			t.Fatalf("jitter(%v) = %v, outside [75%%,125%%]", base, d)
		}
	}
	if jitter(0) != 0 {
		t.Fatal("jitter(0) must be 0")
	}
}
