package easyblobkit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFormatPathUnderKeepsFirstSegment locks the regression fix: a plain path
// whose first segment equals the mount name must be preserved verbatim. Before
// the fix, /artifacts/debian/debian/dists/... had its leading "debian/" stripped
// (the archive key is literally "debian"), so debian pull-through 404'd.
func TestFormatPathUnderKeepsFirstSegment(t *testing.T) {
	cases := []struct {
		path, mount, want string
	}{
		{"/artifacts/debian/debian/dists/trixie/Release", "/artifacts/debian", "debian/dists/trixie/Release"},
		{"/artifacts/debian/ubuntu/dists/noble/Release", "/artifacts/debian", "ubuntu/dists/noble/Release"},
		{"/artifacts/debian/debian-security/dists/trixie-security/Release", "/artifacts/debian", "debian-security/dists/trixie-security/Release"},
		{"/artifacts/apk/v3.24/main/x86_64/APKINDEX.tar.gz", "/artifacts/apk", "v3.24/main/x86_64/APKINDEX.tar.gz"},
		{"/artifacts/rpm/fedora41/repodata/repomd.xml", "/artifacts/rpm", "fedora41/repodata/repomd.xml"},
		{"/artifacts/conda/pkgs/main/linux-64/repodata.json", "/artifacts/conda", "pkgs/main/linux-64/repodata.json"},
		// A repeated segment WITHOUT _hosted is a plain path and must survive.
		{"/artifacts/apk/apk/v3.24/main", "/artifacts/apk", "apk/v3.24/main"},
		{"/artifacts/debian/debian/dists/x", "/artifacts/debian", "debian/dists/x"},
	}
	for _, c := range cases {
		if got := formatPathUnder(c.path, c.mount); got != c.want {
			t.Errorf("formatPathUnder(%q, %q) = %q, want %q", c.path, c.mount, got, c.want)
		}
	}
}

// TestHostedPathUnderOnlyStripsBeforeHosted locks that the repeated-segment
// tolerance applies ONLY when "_hosted" follows, and that plain paths are never
// truncated even when the first segment equals the mount name.
func TestHostedPathUnderOnlyStripsBeforeHosted(t *testing.T) {
	cases := []struct {
		path, mount, want string
	}{
		// Historical doubled form before _hosted is tolerated.
		{"/artifacts/apk/apk/_hosted/myrepo/x86_64/x.apk", "/artifacts/apk", "_hosted/myrepo/x86_64/x.apk"},
		{"/artifacts/debian/debian/_hosted/easy-platform/Packages", "/artifacts/debian", "_hosted/easy-platform/Packages"},
		// Single form unchanged.
		{"/artifacts/apk/_hosted/myrepo/x86_64/x.apk", "/artifacts/apk", "_hosted/myrepo/x86_64/x.apk"},
		// Plain path (no _hosted) keeps the first segment.
		{"/artifacts/apk/apk/v3.24/main", "/artifacts/apk", "apk/v3.24/main"},
		{"/artifacts/debian/debian/dists/trixie/Release", "/artifacts/debian", "debian/dists/trixie/Release"},
	}
	for _, c := range cases {
		if got := hostedPathUnder(c.path, c.mount); got != c.want {
			t.Errorf("hostedPathUnder(%q, %q) = %q, want %q", c.path, c.mount, got, c.want)
		}
	}
}

// TestHostedRequestResolvesHostedAndPlain locks the end-to-end resolution the
// adapters see: hosted addressing (with and without the historical doubled
// segment) resolves to (repo, nativePath, true); a plain path is NOT hosted and
// keeps its first segment.
func TestHostedRequestResolvesHostedAndPlain(t *testing.T) {
	// Hosted, single form.
	rq := httptest.NewRequest(http.MethodPut, "/artifacts/apk/_hosted/myrepo/x86_64/x.apk", nil)
	repo, path, hosted := HostedRequest(rq, "/artifacts/apk")
	if !hosted || repo != "myrepo" || path != "x86_64/x.apk" {
		t.Fatalf("single: repo=%q path=%q hosted=%v", repo, path, hosted)
	}

	// Hosted, historical doubled form.
	rq2 := httptest.NewRequest(http.MethodPut, "/artifacts/apk/apk/_hosted/myrepo/x86_64/x.apk", nil)
	repo2, path2, hosted2 := HostedRequest(rq2, "/artifacts/apk")
	if !hosted2 || repo2 != "myrepo" || path2 != "x86_64/x.apk" {
		t.Fatalf("doubled: repo=%q path=%q hosted=%v", repo2, path2, hosted2)
	}

	// Plain path: NOT hosted, first segment preserved.
	rq3 := httptest.NewRequest(http.MethodGet, "/artifacts/debian/debian/dists/trixie/Release", nil)
	repo3, path3, hosted3 := HostedRequest(rq3, "/artifacts/debian")
	if hosted3 || repo3 != "" || path3 != "debian/dists/trixie/Release" {
		t.Fatalf("plain: repo=%q path=%q hosted=%v", repo3, path3, hosted3)
	}
}
