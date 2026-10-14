package oci

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/easydev-org/easyblobs/core"
)

// --- blob upload session handlers -------------------------------------------

func (a *Adapter) upload(w http.ResponseWriter, r *http.Request, name, session string) {
	if !a.authorize(r, name, easyblobkit.ActionPush) {
		a.challenge(w, name, easyblobkit.ActionPush)
		return
	}
	switch r.Method {
	case http.MethodPost:
		// Mount / single-shot / start session.
		if mnt := r.URL.Query().Get("mount"); mnt != "" {
			if size, _ := a.state.Registry.Blobs.Stat(r.Context(), mnt); size != nil {
				w.Header().Set("Location", "/v2/"+name+"/blobs/"+mnt)
				w.Header().Set("Docker-Content-Digest", mnt)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		if dgst := r.URL.Query().Get("digest"); dgst != "" {
			if _, err := easyblobkit.ParseDigest(dgst); err != nil {
				writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "invalid digest"))
				return
			}
			easyblobkit.LimitBody(w, r)
			// Stream the body into the CAS (no full in-memory copy) and verify
			// the client's digest against the one we computed.
			stored, err := a.state.Registry.StoreStream(r.Context(), r.Body)
			if err != nil {
				if easyblobkit.IsBodyTooLarge(err) {
					writeJSON(w, http.StatusRequestEntityTooLarge, ociError("BLOB_UPLOAD_INVALID", "blob too large"))
					return
				}
				writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "read error"))
				return
			}
			if dgst != stored.Digest {
				// The body was streamed under its true digest; the client
				// named a different one, so drop the bytes we just stored.
				easyblobkit.LogMetaErr("oci reject blob", a.state.Registry.Blobs.Delete(r.Context(), stored.Digest))
				writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "digest does not match content"))
				return
			}
			w.Header().Set("Location", "/v2/"+name+"/blobs/"+dgst)
			w.Header().Set("Docker-Content-Digest", dgst)
			w.WriteHeader(http.StatusCreated)
			return
		}
		// Start a session.
		sid := a.nextSessionID()
		if sid == "" {
			writeJSON(w, http.StatusInternalServerError, ociError("UNKNOWN", "session id generation failed"))
			return
		}
		sessID := "sess-" + sid
		if err := a.state.Registry.Meta.SaveUpload(r.Context(), easyblobkit.UploadRecord{ID: sessID, Format: "oci", Repository: name}); err != nil {
			writeJSON(w, http.StatusInternalServerError, ociError("UNKNOWN", err.Error()))
			return
		}
		w.Header().Set("Location", "/v2/"+name+"/blobs/uploads/"+sessID)
		w.Header().Set("Docker-Upload-UUID", sessID)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodGet:
		u, err := a.state.Registry.Meta.GetUpload(r.Context(), session)
		if err != nil {
			writeJSON(w, http.StatusNotFound, ociError("BLOB_UPLOAD_UNKNOWN", "blob upload unknown"))
			return
		}
		// The part file is authoritative for the resumable offset.
		w.Header().Set("Docker-Upload-UUID", u.ID)
		w.Header().Set("Range", fmt.Sprintf("0-%d", max64(a.uploads.Size(r.Context(), session)-1, 0)))
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPatch:
		// Append the chunk to the on-disk session file.
		u, ok := a.beginUpload(w, r, name, session)
		if !ok {
			return
		}
		easyblobkit.LimitBody(w, r)
		total, err := a.uploads.Append(r.Context(), session, r.Body)
		if err != nil {
			if easyblobkit.IsBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, ociError("BLOB_UPLOAD_INVALID", "chunk too large"))
				return
			}
			writeJSON(w, http.StatusInternalServerError, ociError("UNKNOWN", err.Error()))
			return
		}
		a.saveUploadSize(r.Context(), u, total)
		w.Header().Set("Docker-Upload-UUID", session)
		w.Header().Set("Range", fmt.Sprintf("0-%d", max64(total-1, 0)))
		w.Header().Set("Location", "/v2/"+name+"/blobs/uploads/"+session)
		w.WriteHeader(http.StatusAccepted)
	case http.MethodPut:
		// Final chunk (may carry the remainder of the body) + digest commit.
		u, ok := a.beginUpload(w, r, name, session)
		if !ok {
			return
		}
		easyblobkit.LimitBody(w, r)
		total, err := a.uploads.Append(r.Context(), session, r.Body)
		if err != nil {
			if easyblobkit.IsBodyTooLarge(err) {
				writeJSON(w, http.StatusRequestEntityTooLarge, ociError("BLOB_UPLOAD_INVALID", "final chunk too large"))
				return
			}
			writeJSON(w, http.StatusInternalServerError, ociError("UNKNOWN", err.Error()))
			return
		}
		a.saveUploadSize(r.Context(), u, total)
		dgst := r.URL.Query().Get("digest")
		if dgst == "" {
			writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "digest parameter missing"))
			return
		}
		if _, err := easyblobkit.ParseDigest(dgst); err != nil {
			writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "invalid digest"))
			return
		}
		// Stream the staged bytes into the blob store (no full in-memory copy);
		// BlobStore.Put verifies the declared digest in the same pass, so a
		// mismatch fails here.
		if err := a.commitUpload(r.Context(), session, dgst); err != nil {
			if errors.Is(err, easyblobkit.ErrDigestMismatch) {
				writeJSON(w, http.StatusBadRequest, ociError("DIGEST_INVALID", "digest does not match content"))
				return
			}
			writeJSON(w, http.StatusInternalServerError, ociError("UNKNOWN", err.Error()))
			return
		}
		_ = a.state.Registry.Meta.DeleteUpload(r.Context(), session)
		w.Header().Set("Location", "/v2/"+name+"/blobs/"+dgst)
		w.Header().Set("Docker-Content-Digest", dgst)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		a.uploads.Delete(r.Context(), session)
		_ = a.state.Registry.Meta.DeleteUpload(r.Context(), session)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// beginUpload validates that the session exists AND belongs to the named
// repository (a session id leaked to another client cannot be cross-mounted),
// writing the appropriate error response when invalid.
func (a *Adapter) beginUpload(w http.ResponseWriter, r *http.Request, name, session string) (easyblobkit.UploadRecord, bool) {
	u, err := a.state.Registry.Meta.GetUpload(r.Context(), session)
	if err != nil {
		writeJSON(w, http.StatusNotFound, ociError("BLOB_UPLOAD_UNKNOWN", "blob upload unknown"))
		return easyblobkit.UploadRecord{}, false
	}
	if u.Repository != name || u.Format != "oci" {
		writeJSON(w, http.StatusBadRequest, ociError("BLOB_UPLOAD_INVALID", "upload session belongs to a different repository"))
		return easyblobkit.UploadRecord{}, false
	}
	return u, true
}

// saveUploadSize persists the running byte count for resume reporting.
func (a *Adapter) saveUploadSize(ctx context.Context, u easyblobkit.UploadRecord, total int64) {
	u.Bytes = total
	_ = a.state.Registry.Meta.SaveUpload(ctx, u)
}

// commitUpload streams the staged session bytes into the CAS (no full
// in-memory copy), then drops the staging. It is backend-agnostic: the
// UploadStore supplies the bytes in order, the BlobStore owns hashing/dedup.
func (a *Adapter) commitUpload(ctx context.Context, session, digest string) error {
	rc, err := a.uploads.Open(ctx, session)
	if err != nil {
		return err
	}
	if rc == nil {
		return fmt.Errorf("upload session %s has no staged bytes", session)
	}
	defer func() { _ = rc.Close() }()
	if _, _, err := a.state.Registry.Blobs.Put(ctx, rc, digest); err != nil {
		return err
	}
	a.uploads.Delete(ctx, session)
	return nil
}

func (a *Adapter) listReferrers(w http.ResponseWriter, r *http.Request, name, subject string) {
	if !a.authorize(r, name, easyblobkit.ActionPull) {
		a.challenge(w, name, easyblobkit.ActionPull)
		return
	}
	filter := r.URL.Query().Get("artifactType")
	var manifestList []map[string]any
	versions, _ := a.state.Registry.Meta.ListVersions(r.Context(), "oci", name)
	for _, v := range versions {
		if _, err := easyblobkit.ParseDigest(v); err != nil {
			continue
		}
		art, err := a.state.Registry.Meta.Get(r.Context(), "oci", name, v)
		if err != nil {
			continue
		}
		at := manifestArtifactType(art.Proprietary)
		if at == "" || manifestSubject(art.Proprietary) != subject {
			continue
		}
		if filter != "" && at != filter {
			continue
		}
		manifestList = append(manifestList, map[string]any{
			"mediaType": art.MediaType, "digest": art.Digest, "size": len(art.Proprietary),
			"artifactType": at, "annotations": map[string]any{},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": manifestList,
	})
}

// nextSessionID returns a unique upload-session id (crypto/rand). A
// generation failure returns "" which the caller treats as a 500.
func (a *Adapter) nextSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
