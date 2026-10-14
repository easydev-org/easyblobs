package easyblobkit

import (
	"strings"

	"github.com/easydev-org/easyblobs/targets"
)

// Reserved path segments (the "_<kind>" family). HostedSegment ("_hosted") is
// defined in hosted.go; these are its siblings, so one grammar covers
// repository selection, upstream selection and hosted vs cached:
//
//	/artifacts/<proto>/<tail>                        default upstream
//	/artifacts/<proto>/_upstream/<host>/<tail>       explicit upstream (host)
//	/artifacts/<proto>/_repo/<repo>/<tail>           explicit repository
//	/artifacts/<proto>/_hosted/<repo>/<tail>         self-published repository
//	/artifacts/<proto>/-/<repo>/<tail>               legacy explicit repository
//
// The first segment is always the protocol (which adapter), so a host shared by
// several protocols (buf.build, github.com) is disambiguated by construction —
// no host table lookup is needed to ROUTE.
const (
	// UpstreamSegment names an explicit upstream host for a request.
	UpstreamSegment = "_upstream"
	// RepoSegment names an explicit repository (sibling of the legacy
	// "/-/<repo>/" marker).
	RepoSegment = "_repo"
)

// SplitUpstream strips a leading "_upstream/<host>/" segment from a
// format-relative path and returns the host and the remaining tail. ok is false
// when the segment is absent or the host is not a public DNS name — the SSRF
// guard, so an upstream can never point at a cluster-local name or a bare
// address. Only the host is written in the path; any base path prefix comes from
// the target table (a declared target) or is absent (an ad-hoc public host).
func SplitUpstream(p string) (host, rest string, ok bool) {
	p = strings.Trim(p, "/")
	first, after, split := SplitRepo(p)
	if !split || first != UpstreamSegment {
		return "", p, false
	}
	host, rest, ok = SplitRepo(after)
	if !ok || host == "" || strings.Contains(host, "/") || !targets.IsPublicHost(host) {
		return "", p, false
	}
	return host, rest, true
}

// SplitExplicitRepoSegment strips a leading "_repo/<repo>/" segment — the
// reserved-segment sibling of the "/-/<repo>/" marker. It names a repository
// (content boundary) without the hosted flag.
func SplitExplicitRepoSegment(p string) (repo, rest string, ok bool) {
	p = strings.Trim(p, "/")
	first, after, split := SplitRepo(p)
	if !split || first != RepoSegment {
		return "", p, false
	}
	repo, rest, ok = SplitRepo(after)
	if !ok || repo == "" {
		return "", p, false
	}
	return repo, rest, true
}
