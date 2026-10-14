package store

import (
	"context"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// TestListPackagesHostedAndNamespace locks the /artifacts/system/packages
// projection: Source/Hosted distinguish a self-published package ("push") from
// an upstream cache ("pull"), and Namespace/Repo split the repository key.
func TestListPackagesHostedAndNamespace(t *testing.T) {
	s, err := OpenSQLite(t.TempDir() + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	// Self-published OCI image (namespaced) and an upstream cache (bare repo).
	if err := s.Put(ctx, easyblobkit.Artifact{Format: "oci", Repository: "easy-vcs/agent-gateway", Version: "v1", Source: "push"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, easyblobkit.Artifact{Format: "npm", Repository: "left-pad", Version: "1.3.0", Source: "pull"}); err != nil {
		t.Fatal(err)
	}

	pkgs, err := s.ListPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byRepo := map[string]easyblobkit.PackageSummary{}
	for _, p := range pkgs {
		byRepo[p.Repository] = p
	}

	img, ok := byRepo["easy-vcs/agent-gateway"]
	if !ok {
		t.Fatal("missing oci package")
	}
	if !img.Hosted || img.Source != "push" {
		t.Errorf("oci: hosted=%v source=%q, want hosted=true source=push", img.Hosted, img.Source)
	}
	if img.Namespace != "easy-vcs" || img.Repo != "agent-gateway" {
		t.Errorf("oci split: ns=%q repo=%q", img.Namespace, img.Repo)
	}

	cache, ok := byRepo["left-pad"]
	if !ok {
		t.Fatal("missing npm package")
	}
	if cache.Hosted || cache.Source != "pull" {
		t.Errorf("npm cache: hosted=%v source=%q, want hosted=false source=pull", cache.Hosted, cache.Source)
	}
	if cache.Namespace != "" || cache.Repo != "left-pad" {
		t.Errorf("npm split: ns=%q repo=%q, want ns=\"\" repo=left-pad", cache.Namespace, cache.Repo)
	}
}
