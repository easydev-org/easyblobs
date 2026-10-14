package s3blob

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// UploadStore is an S3-backed staging area for in-progress blob uploads. Each
// chunk is one object under <prefix>/uploads/<session>/<seq>, and Open
// concatenates them in sequence order. Staging chunk-per-object (rather than
// S3 multipart) avoids multipart's 5 MiB minimum-part rule, which small OCI
// layers would violate, and needs no session bookkeeping beyond the object
// list.
type UploadStore struct {
	client *s3.Client
	bucket string
	prefix string
}

// UploadStore returns an UploadStore over this Store's client/bucket/prefix, so
// OCI upload staging lands on the same S3 (garage) as the CAS.
func (s *Store) UploadStore() *UploadStore {
	return &UploadStore{client: s.client, bucket: s.bucket, prefix: s.prefix}
}

func (u *UploadStore) sessionPrefix(session string) (string, error) {
	if session == "" || strings.ContainsAny(session, "/\\") || strings.Contains(session, "..") {
		return "", fmt.Errorf("s3blob: invalid session id")
	}
	base := "uploads/" + session + "/"
	if u.prefix != "" {
		base = u.prefix + "/" + base
	}
	return base, nil
}

// list returns the session's chunk keys (lexicographic = sequence order) and
// their sizes.
func (u *UploadStore) list(ctx context.Context, session string) ([]string, int64, error) {
	pfx, err := u.sessionPrefix(session)
	if err != nil {
		return nil, 0, err
	}
	var keys []string
	var total int64
	var token *string
	for {
		out, err := u.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: &u.bucket, Prefix: &pfx, ContinuationToken: token,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, o := range out.Contents {
			keys = append(keys, aws.ToString(o.Key))
			total += aws.ToInt64(o.Size)
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return keys, total, nil
}

func (u *UploadStore) Append(ctx context.Context, session string, r io.Reader) (int64, error) {
	keys, total, err := u.list(ctx, session)
	if err != nil {
		return 0, err
	}
	pfx, err := u.sessionPrefix(session)
	if err != nil {
		return 0, err
	}
	// Sequence number: one past the current chunk count (zero-padded so
	// lexicographic listing preserves order).
	key := fmt.Sprintf("%s%010d", pfx, len(keys))
	// Spool the chunk to a TEMP FILE, never to memory: an OCI client may send a
	// whole multi-GB layer as ONE chunk, and buffering it in RAM OOM-kills the
	// process (dropping every other request).
	tmp, n, err := spoolToTemp(r)
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()
	if _, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &u.bucket, Key: &key, Body: tmp, ContentLength: aws.Int64(n),
	}); err != nil {
		return 0, err
	}
	return total + n, nil
}

// spoolToTemp streams r to a fresh seekable temp file (never into memory) and
// rewinds it, so a multi-GB chunk can be handed to PutObject with a known
// Content-Length without buffering it in RAM. The caller removes/closes it.
func spoolToTemp(r io.Reader) (*os.File, int64, error) {
	tmp, err := os.CreateTemp("", "artifact-s3-upload-*")
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, 0, err
	}
	return tmp, n, nil
}

func (u *UploadStore) Size(ctx context.Context, session string) int64 {
	_, total, err := u.list(ctx, session)
	if err != nil {
		return 0
	}
	return total
}

func (u *UploadStore) Open(ctx context.Context, session string) (io.ReadCloser, error) {
	keys, _, err := u.list(ctx, session)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	readers := make([]io.Reader, 0, len(keys))
	closers := make([]io.Closer, 0, len(keys))
	for _, k := range keys {
		obj, err := u.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &u.bucket, Key: aws.String(k)})
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			return nil, err
		}
		readers = append(readers, obj.Body)
		closers = append(closers, obj.Body)
	}
	return &multiReadCloser{r: io.MultiReader(readers...), closers: closers}, nil
}

func (u *UploadStore) Delete(ctx context.Context, session string) {
	keys, _, err := u.list(ctx, session)
	if err != nil {
		return
	}
	for _, k := range keys {
		_, _ = u.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &u.bucket, Key: aws.String(k)})
	}
}

func (u *UploadStore) Sweep(ctx context.Context, olderThan time.Duration) {
	base := "uploads/"
	if u.prefix != "" {
		base = u.prefix + "/" + base
	}
	cutoff := time.Now().Add(-olderThan)
	var token *string
	for {
		out, err := u.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: &u.bucket, Prefix: &base, ContinuationToken: token,
		})
		if err != nil {
			return
		}
		for _, o := range out.Contents {
			if o.LastModified != nil && o.LastModified.Before(cutoff) {
				_, _ = u.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &u.bucket, Key: o.Key})
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
}

// multiReadCloser concatenates a set of object bodies and closes them all.
type multiReadCloser struct {
	r       io.Reader
	closers []io.Closer
}

func (m *multiReadCloser) Read(p []byte) (int, error) { return m.r.Read(p) }

func (m *multiReadCloser) Close() error {
	var err error
	for _, c := range m.closers {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}
