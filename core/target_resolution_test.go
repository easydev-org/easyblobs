package easyblobkit

import (
	"context"
	"testing"

	"github.com/easydev-org/easyblobs/targets"
)

// targetUpstreams builds the same table the server builds: the target registry
// plus its legacy projection and empty policy maps.
func targetUpstreams() *Upstreams {
	reg := targets.NewRegistry()
	return &Upstreams{
		Targets:   reg,
		Defaults:  reg.LegacyDefaults(),
		Overrides: map[string]string{},
		Proxy:     map[string]string{},
	}
}

// TestResolveBasePriority locks the invariant order:
// per-repo override > explicitly mounted target > table default.
func TestResolveBasePriority(t *testing.T) {
	u := targetUpstreams()
	r := &Registry{Upstreams: u}

	// table default.
	ctx := WithRepoScope(context.Background(), RepoScope{Format: "maven", Name: "guava"})
	base, _, ok := r.resolveBase(ctx, "maven", "guava")
	if !ok || base != "https://repo.maven.apache.org/maven2" {
		t.Fatalf("default: %q ok=%v", base, ok)
	}

	// a per-repo override beats the default.
	u.SetRepo("maven", "com.google.guava", "https://mirror.corp/m2", "")
	base, _, ok = r.resolveBase(ctx, "maven", "com.google.guava")
	if !ok || base != "https://mirror.corp/m2" {
		t.Fatalf("override: %q ok=%v", base, ok)
	}
}

// TestAirGapStillReturnsNothing confirms the air-gap flag wins over targets.
func TestAirGapStillReturnsNothing(t *testing.T) {
	u := targetUpstreams()
	u.AirGap = true
	if u.Get("maven") != "" {
		t.Error("air-gap Get returned an upstream")
	}
	ctx := WithRepoScope(context.Background(), RepoScope{Format: "maven", Name: "guava"})
	if _, _, ok := (&Registry{Upstreams: u}).resolveBase(ctx, "maven", "guava"); ok {
		t.Error("air-gap resolved an upstream")
	}
}

// TestNamedTargetFromScope locks the explicit-upstream resolution path: a
// request under /artifacts/maven/_upstream/dl.google.com/... must select Google
// Maven.
func TestNamedTargetFromScope(t *testing.T) {
	u := targetUpstreams()
	r := &Registry{Upstreams: u}
	ctx := WithRepoScope(context.Background(), RepoScope{
		Format: "maven", Upstream: "dl.google.com", Name: "gradle",
	})
	base, _, ok := r.resolveBase(ctx, "maven", "gradle")
	if !ok {
		t.Fatal("named target did not resolve")
	}
	if base != "https://dl.google.com/dl/android/maven2" {
		t.Errorf("base = %q, want Google Maven", base)
	}
}

// TestDefaultTargetFallsThrough keeps the default target from being mistaken
// for a named one: /artifacts/cargo/... resolves to the table default.
func TestDefaultTargetFallsThrough(t *testing.T) {
	u := targetUpstreams()
	r := &Registry{Upstreams: u}
	ctx := WithRepoScope(context.Background(), RepoScope{
		Format: "cargo", Target: "cargo", TargetShared: true, Name: "serde",
	})
	base, _, ok := r.resolveBase(ctx, "cargo", "serde")
	if !ok || base != "https://crates.io" {
		t.Errorf("base = %q ok=%v, want crates.io", base, ok)
	}
}
