package easyblobkit

import (
	"context"
	"time"
)

// TimeRFC3339 formats a time as an RFC3339 UTC string, or "" for the zero time.
// Protocol adapters emit timestamps in RFC3339 (npm time, PyPI upload-time,
// Helm created, ...); this is the one place that formats them.
func TimeRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// PublishTime returns an artifact's publish time as an RFC3339 string, falling
// back to its stored (cache) time, and finally to fallback (typically now) so a
// protocol response never carries a zero/invalid timestamp. It replaces the
// hardcoded "2024-01-01T00:00:00Z" placeholders the adapters used.
func PublishTime(a Artifact, fallback time.Time) string {
	if !a.PublishedAt.IsZero() {
		return TimeRFC3339(a.PublishedAt)
	}
	if !a.StoredAt.IsZero() {
		return TimeRFC3339(a.StoredAt)
	}
	if !fallback.IsZero() {
		return TimeRFC3339(fallback)
	}
	return TimeRFC3339(time.Now())
}

// ArtifactTime returns the RFC3339 timestamp to advertise for one
// (format, repository, version), using the stored row's publish time when
// present. ok is false when the version is unknown locally.
func (r *Registry) ArtifactTime(ctx context.Context, format, repository, version string) (string, bool) {
	a, err := r.Meta.Get(ctx, format, repository, version)
	if err != nil {
		return "", false
	}
	return PublishTime(a, time.Time{}), true
}
