package easyblobkit

import (
	"net/http"
	"time"
)

// Shared cache mechanics for the two cache engines (netcache's URL cache and
// the path-tree cache). Their POLICIES differ on purpose:
//
//   - netcache keys on the request URI and, when a mutable entry is stale and
//     carries no validator, simply re-fetches (a URL may have changed).
//   - pathcache keys on (format, repository, version) and, when a mutable
//     entry is stale with no validator, keeps serving the cached bytes (a
//     100MB+ tree index cannot be re-downloaded on every TTL expiry).
//
// The mechanics below are identical in both: building the conditional request
// from a stored validator, applying a 304's refreshed validators/TTL, and
// assembling the replayable artifact from an upstream response.

// conditionalHeaders returns the If-None-Match / If-Modified-Since headers for a
// revalidation request, from a cached entry's stored validators. Empty when the
// entry carries none (then a full fetch is required).
func conditionalHeaders(prev Artifact) map[string]string {
	h := map[string]string{}
	if prev.ETag != "" {
		h["If-None-Match"] = prev.ETag
	}
	if prev.LastModified != "" {
		h["If-Modified-Since"] = prev.LastModified
	}
	return h
}

// hasValidator reports whether a cached entry can be conditionally revalidated.
func hasValidator(art Artifact) bool { return art.ETag != "" || art.LastModified != "" }

// refreshFrom304 updates a cached entry after a 304: it adopts any validators
// the origin returned and extends the TTL (unless immutable). The caller
// persists art.
func refreshFrom304(art *Artifact, h http.Header, ttl time.Duration) {
	if v := h.Get("ETag"); v != "" {
		art.ETag = v
	}
	if v := h.Get("Last-Modified"); v != "" {
		art.LastModified = v
		if t, err := http.ParseTime(v); err == nil {
			art.PublishedAt = t
		}
	}
	// StoredAt is when we first cached it; a 304 does not change that. Backfill
	// it for entries that predate the field.
	if art.StoredAt.IsZero() {
		art.StoredAt = time.Now()
	}
	if art.CacheControl != "immutable" {
		art.ExpiresAt = time.Now().Add(ttl)
	}
}

// artifactFromResponse assembles a cache entry from an upstream response and
// the bytes already streamed into the CAS. immutable pins CacheControl; else
// ExpiresAt is now+ttl.
func artifactFromResponse(format, repo, version string, digest string, size int64, h http.Header, mediaType, blobName string, immutable bool, ttl time.Duration) Artifact {
	now := time.Now()
	art := Artifact{
		Format: format, Repository: repo, Version: version,
		MediaType:       mediaType,
		Digest:          digest,
		ETag:            h.Get("ETag"),
		LastModified:    h.Get("Last-Modified"),
		ContentEncoding: h.Get("Content-Encoding"),
		Blobs:           []Descriptor{{Digest: digest, Size: size, Name: blobName}},
		Source:          "pull",
		StoredAt:        now,
	}
	// PublishedAt: the upstream's Last-Modified is the best generic signal.
	// Protocol adapters that learn a more precise time (npm `time`, PyPI
	// `upload_time`, OCI `created`) overwrite it before persisting.
	if t, err := http.ParseTime(art.LastModified); err == nil {
		art.PublishedAt = t
	}
	if immutable {
		art.CacheControl = "immutable"
	} else {
		art.ExpiresAt = time.Now().Add(ttl)
	}
	return art
}

// isFresh reports whether a cached entry may be served without revalidation:
// immutable entries never expire, and a zero ExpiresAt means "no TTL" (a
// permanent / ad-hoc entry).
func isFresh(art Artifact, now time.Time) bool {
	return art.CacheControl == "immutable" || art.ExpiresAt.IsZero() || now.Before(art.ExpiresAt)
}

// Fresh reports whether a cached entry may be served without revalidation. It
// is the exported form of isFresh for adapters (OCI) that hold their own cache
// rows in the shared IndexStore.
func Fresh(art Artifact, now time.Time) bool { return isFresh(art, now) }

// ImmutableTag marks an entry whose key looks up content that can never change
// (a content-addressed digest). Immutable entries never expire.
const ImmutableTag = "immutable"

// headerFromArtifact reconstructs the replay headers from a cached entry.
func headerFromArtifact(art Artifact) http.Header {
	h := http.Header{}
	if art.MediaType != "" {
		h.Set("Content-Type", art.MediaType)
	}
	if art.ContentEncoding != "" {
		h.Set("Content-Encoding", art.ContentEncoding)
	}
	if art.ETag != "" {
		h.Set("ETag", art.ETag)
	}
	if art.LastModified != "" {
		h.Set("Last-Modified", art.LastModified)
	}
	return h
}
