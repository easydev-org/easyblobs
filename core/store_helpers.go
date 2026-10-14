package easyblobkit

import (
	"bytes"
	"context"
	"io"
	"strings"
)

// StoreAndHash writes into the blob store (dedup by sha256) and returns a summary.
// The store computes and records the hashes, so nothing is hashed twice.
func (r *Registry) StoreAndHash(ctx context.Context, data []byte) (Stored, error) {
	stored, _, err := r.Blobs.Put(ctx, bytes.NewReader(data), "")
	return stored, err
}

// StoreStream consumes a reader into the CAS without buffering the whole body
// in memory. The store streams it once, computing and recording the hash set in
// that pass (no double hash, no post-write re-read). This is the upload-path
// counterpart to StoreAndHash.
func (r *Registry) StoreStream(ctx context.Context, body io.Reader) (Stored, error) {
	stored, _, err := r.Blobs.Put(ctx, body, "")
	return stored, err
}

// BlobHashes returns a blob's hash set. It reads the set recorded by the store
// at write time (no re-read); only a blob written before that feature existed
// falls back to one recompute from the stored bytes.
func (r *Registry) BlobHashes(ctx context.Context, digest string) (Hashes, error) {
	if h, ok, err := r.Blobs.Hashes(ctx, digest); err == nil && ok {
		return h, nil
	}
	rd, err := r.Blobs.Open(ctx, digest)
	if err != nil {
		return Hashes{}, err
	}
	if rd == nil {
		return Hashes{}, ErrBlobUnknown
	}
	defer func() { _ = rd.Close() }()
	return ComputeHashes(rd)
}

// ReadBlobPrefix reads at most n bytes from the start of a blob, streaming
// rather than loading it whole. Index generators use it to parse the small
// control/header region of a large package without reading every byte.
// (nil, nil) when the blob is absent.
func (r *Registry) ReadBlobPrefix(ctx context.Context, digest string, n int64) ([]byte, error) {
	rd, err := r.Blobs.Open(ctx, digest)
	if err != nil || rd == nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()
	if n <= 0 {
		n = 1 << 20
	}
	return io.ReadAll(io.LimitReader(rd, n))
}

// StorePathBlob is the shared "cache one fetched path" flow for the path-tree
// protocols (apk/debian/rpm/conda/nix/...): store the bytes in the CAS by
// digest, then index them under (format, repository, version). It is
// best-effort: a cache write failure never fails the response, so the caller
// keeps serving the bytes it already has.
//
// blobName is recorded on the descriptor (adapters differ: some use the
// basename, some the full relative path). Empty falls back to version.
func (r *Registry) StorePathBlob(ctx context.Context, format, repository, version, blobName, mediaType string, data []byte) string {
	stored, err := r.StoreAndHash(ctx, data)
	if err != nil {
		return ""
	}
	if blobName == "" {
		blobName = baseNameOf(version)
	}
	LogMetaErr(format+" cache", r.Meta.Put(ctx, Artifact{
		Format: format, Repository: repository, Version: version,
		MediaType: mediaType, Digest: stored.Digest,
		Blobs:  []Descriptor{{Digest: stored.Digest, Size: stored.Size, Name: blobName}},
		Source: "pull",
	}))
	return stored.Digest
}

// VersionInput describes one artifact version to persist: the identity plus the
// bytes. It is the input to StoreVersion, the shared publish/pull cache write
// every flat adapter used to hand-roll.
type VersionInput struct {
	Format     string
	Repository string
	Version    string
	// Filename is the descriptor name (e.g. "pkg-1.2.3.tar.gz"). Empty uses
	// the version as the name.
	Filename string
	// Source is "push" (published) or "pull" (cached upstream).
	Source string
	// Body is the artifact content, streamed into the CAS (no in-memory copy).
	// Empty stores no blob (index-only row). Mutually exclusive with Data.
	Body io.Reader
	// Data is an in-memory alternative to Body. Mutually exclusive with Body.
	Data []byte
	// ExtraBlobs are additional blobs to index alongside the primary one
	// (rare; e.g. a detached signature). Optional.
	ExtraBlobs []Descriptor
	// Proprietary is opaque per-format metadata (hex inner_checksum, composer
	// autoload, ...). Optional.
	Proprietary []byte
	// MediaType is recorded on the row. Optional.
	MediaType string
	// DefaultVersion is applied when Version is empty (per-format, e.g.
	// "0.1.0"). Empty leaves Version as-is.
	DefaultVersion string
}

// StoreVersion persists one artifact version: stream the content into the CAS,
// REPLACE any existing descriptor of the same filename (a re-publish of a
// filename supersedes it; other filenames of the same version, e.g. a jar's
// pom/sha1, are preserved), and upsert the index row. It is the single
// implementation behind every flat adapter's publish/cache write.
//
// An orphaned blob from a replaced descriptor is NOT deleted eagerly: another
// version may share the digest, and the background reaper reclaims true
// orphans. Index errors are logged, never fatal to the response.
func (r *Registry) StoreVersion(ctx context.Context, in VersionInput) {
	if in.Version == "" && in.DefaultVersion != "" {
		in.Version = in.DefaultVersion
	}
	name := in.Filename
	if name == "" {
		name = in.Version
	}
	art, _ := r.Meta.Get(ctx, in.Format, in.Repository, in.Version)
	art.Format, art.Repository, art.Version = in.Format, in.Repository, in.Version
	art.MediaType, art.Source, art.Proprietary = in.MediaType, in.Source, in.Proprietary

	// Replace the descriptor with the same name; keep the others.
	kept := art.Blobs[:0:0]
	for _, b := range art.Blobs {
		if b.Name != name {
			kept = append(kept, b)
		}
	}
	art.Blobs = kept

	body := in.Body
	if body == nil && len(in.Data) > 0 {
		body = bytes.NewReader(in.Data)
	}
	if body != nil {
		if stored, _, err := r.Blobs.Put(ctx, body, ""); err == nil && stored.Size > 0 {
			art.Digest = stored.Digest
			art.Blobs = append(art.Blobs, Descriptor{Digest: stored.Digest, Size: stored.Size, Name: name})
		} else if err != nil {
			LogMetaErr(in.Format+" store blob", err)
		}
	}
	art.Blobs = append(art.Blobs, in.ExtraBlobs...)
	LogMetaErr(in.Format+" meta put", r.Meta.Put(ctx, art))
}

// DeleteVersionFile removes ONE filename (descriptor) from a version and, when
// the digest is no longer referenced anywhere, deletes the CAS blob. It is the
// targeted delete the flat stores lack: removing a single large file (e.g. a
// golden disk) without touching the other files of the same version.
//
// It reports whether the blob was deleted (vs merely unindexed because another
// artifact still references it). A missing file is not an error (deleted=false).
func (r *Registry) DeleteVersionFile(ctx context.Context, format, repository, version, filename string) (blobDeleted bool, err error) {
	art, gerr := r.Meta.Get(ctx, format, repository, version)
	if gerr != nil {
		return false, gerr
	}
	kept := make([]Descriptor, 0, len(art.Blobs))
	var removed []string
	for _, b := range art.Blobs {
		if b.Name == filename {
			removed = append(removed, b.Digest)
			continue
		}
		kept = append(kept, b)
	}
	if len(removed) == 0 {
		return false, nil // nothing to delete
	}
	art.Blobs = kept
	// If the primary digest was the removed file, clear it.
	for _, d := range removed {
		if art.Digest == d {
			art.Digest = ""
		}
	}
	if err := r.Meta.Put(ctx, art); err != nil {
		return false, err
	}
	// Delete the blob(s) only when nothing else references them.
	refs, rerr := r.Meta.ReferencedDigests(ctx)
	if rerr != nil {
		return false, nil // conservative: leave the blob for the reaper
	}
	for _, d := range removed {
		if d == "" || refs[d] {
			continue
		}
		if derr := r.Blobs.Delete(ctx, d); derr == nil {
			blobDeleted = true
		} else {
			LogMetaErr(format+" blob delete", derr)
		}
	}
	return blobDeleted, nil
}

// baseNameOf returns the final path segment ("b/bash.rpm" -> "bash.rpm").
func baseNameOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
