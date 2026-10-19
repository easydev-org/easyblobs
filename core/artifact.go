package easyblobkit

import "time"

type Hashes struct {
	SHA256 string
	SHA1   string
	MD5    string
	SHA512 string
}

type Descriptor struct {
	Digest    string
	MediaType string
	Size      int64
	Name      string
}

func (d Descriptor) Hex() string {
	if s, ok := hexOfDigest(d.Digest); ok {
		return s
	}
	return d.Digest
}

func hexOfDigest(d string) (string, bool) {
	for i := 0; i < len(d); i++ {
		if d[i] == ':' {
			return d[i+1:], true
		}
	}
	return "", false
}

func (d Descriptor) IsEmpty() bool { return d.Digest == "" }

type Artifact struct {
	Format      string
	Repository  string
	Version     string
	MediaType   string
	Proprietary []byte
	Digest      string
	Blobs       []Descriptor
	Source      string
	// Target is the target id the content was fetched through ("maven", or a
	// mirror id such as "maven.google"; "ghcr.io" for OCI). It is provenance
	// only: the primary key stays (Format, Repository, Version), so mirrors of
	// one protocol still share and dedupe by digest. Empty when the adapter did
	// not record one.
	Target string
	// HTTP response metadata, recorded for byte-exact replay and revalidation
	// of cached URIs (the netcache protocol). None of these are key material.
	ETag            string
	LastModified    string
	ContentEncoding string
	// CacheControl is "immutable" for content whose URL denotes a fixed
	// revision (a digest/hash in the URI), which is never revalidated.
	CacheControl string
	// ExpiresAt, when non-zero, is when a non-immutable entry must be
	// revalidated before being served again.
	ExpiresAt time.Time
	// StoredAt is when this version entered our store (fetch time for a
	// pull-through cache, upload time for a hosted publish). Zero when unknown
	// (rows written before this field existed).
	StoredAt time.Time
	// PublishedAt is when the upstream published this version, when we can
	// learn it: a protocol field (npm packument `time`, PyPI `upload_time`, OCI
	// config `created`, Helm `created`) if present, else the HTTP
	// `Last-Modified`. Zero when unknown. It is the "package updated" time; for
	// a hosted publish it falls back to StoredAt.
	PublishedAt time.Time
}

type PackageSummary struct {
	Format     string `json:"format"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
	MediaType  string `json:"media_type,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Size       int64  `json:"size"`
	// Source is how the row was written: "push" (a client published it —
	// self-hosted) or "pull" (cached from an upstream). Empty for rows written
	// before this field existed.
	Source string `json:"source,omitempty"`
	// Hosted is true when the package was self-published (Source == "push"),
	// as opposed to a pull-through upstream cache. It is the flag a browsing UI
	// uses to show only what the platform published.
	Hosted bool `json:"hosted"`
	// Namespace is the owning namespace: the first segment of the repository
	// key (e.g. an OCI image "easy-vcs/agent-gateway" -> "easy-vcs"; npm
	// "@acme/ui" -> "@acme"; maven "org.slf4j/slf4j-api" -> "org.slf4j"). Empty
	// when the repository has a single segment (the default namespace).
	Namespace string `json:"namespace,omitempty"`
	// Repo is the owning repository name: the repository key minus the
	// namespace ("easy-vcs/agent-gateway" -> "agent-gateway"). A UI joins
	// (Namespace, Repo) to a repository and lists its packages/releases.
	Repo string `json:"repo,omitempty"`
	// StoredAt is when this version entered the store (unix seconds; 0 when
	// unknown). PublishedAt is the upstream publish time when known (unix
	// seconds; 0 when unknown) — the "package updated" time.
	StoredAt    int64 `json:"stored_at,omitempty"`
	PublishedAt int64 `json:"published_at,omitempty"`
}
