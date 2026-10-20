package debian

import (
	"context"
	"net/http"
	"strings"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// by-hash support.
//
// When a Release file carries "Acquire-By-Hash: yes", apt fetches indexes by
// CONTENT HASH instead of by name:
//
//	dists/<suite>/<comp>/binary-<arch>/by-hash/SHA256/<hex>
//
// That makes index updates atomic (the bytes at a hash path never change) and
// is what Debian/Ubuntu publish. A mirror must serve those paths or apt fails.
//
// easyblobs is a content-addressed store, so a by-hash path is just a digest
// lookup: <hex> IS the sha256 of the bytes, i.e. digest "sha256:<hex>". We serve
// it from the CAS when that blob is already cached (it is, right after apt
// fetched the index by name), and fall back to the upstream otherwise.

// parseByHash recognizes a by-hash index path and returns the algorithm and
// hex hash. Path form: .../by-hash/<ALGO>/<hex> (ALGO is SHA256 in practice).
func parseByHash(rest string) (algo, hexHash string, ok bool) {
	i := strings.Index(rest, "/by-hash/")
	if i < 0 {
		return "", "", false
	}
	tail := rest[i+len("/by-hash/"):]
	j := strings.IndexByte(tail, '/')
	if j <= 0 || j+1 >= len(tail) {
		return "", "", false
	}
	algo, hexHash = tail[:j], tail[j+1:]
	if !isHex(hexHash) {
		return "", "", false
	}
	switch strings.ToUpper(algo) {
	case "SHA256":
		return "sha256", hexHash, true
	case "SHA512":
		return "sha512", hexHash, true
	case "SHA1":
		return "sha1", hexHash, true
	}
	return "", "", false
}

func isHex(s string) bool {
	if len(s) < 8 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// serveByHash answers a by-hash request from the CAS when the blob is cached,
// returning true when it served. It only serves a digest that belongs to this
// archive (one of the cached index files), so it cannot be used as an
// arbitrary-blob oracle.
func (s *State) serveByHash(w http.ResponseWriter, r *http.Request, archive, rest, ct string) bool {
	algo, hexHash, ok := parseByHash(rest)
	if !ok {
		return false
	}
	digest := algo + ":" + strings.ToLower(hexHash)
	if !s.repoHasDigest(r.Context(), archive, digest) {
		return false
	}
	return easyblobkit.ServeBlobAt(w, r, s.Registry.Blobs, r.Context(), digest, ct)
}

// repoHasDigest reports whether any cached row of archive references digest.
func (s *State) repoHasDigest(ctx context.Context, archive, digest string) bool {
	arts, err := s.Registry.Meta.ListArtifacts(ctx, "debian", archive)
	if err != nil {
		return false
	}
	for i := range arts {
		for _, b := range arts[i].Blobs {
			if b.Digest == digest {
				return true
			}
		}
	}
	return false
}
