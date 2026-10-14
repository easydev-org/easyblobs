package easyblobkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UploadStore stages an in-progress (multi-chunk) blob upload before it is
// committed to the CAS. It is the seam that lets OCI upload state live off the
// local disk: the filesystem backend appends to one file per session, the S3
// backend stages one object per chunk and concatenates them on Open. The
// committed bytes always end up in the BlobStore, so the CAS contract is
// unchanged.
//
// Callers must serialize the operations for one session (the OCI adapter holds
// a per-session lock); the store itself need not be safe for concurrent use of
// the SAME session, but different sessions are independent.
type UploadStore interface {
	// Append stages the next chunk and returns the session's total size.
	Append(ctx context.Context, session string, r io.Reader) (int64, error)
	// Size returns the session's staged byte count (0 when empty/absent).
	Size(ctx context.Context, session string) int64
	// Open returns the staged bytes in chunk order, for verification and for
	// streaming into the BlobStore. (nil, nil) when the session is absent.
	Open(ctx context.Context, session string) (io.ReadCloser, error)
	// Delete drops the session's staged bytes (idempotent).
	Delete(ctx context.Context, session string)
	// Sweep drops sessions untouched for longer than olderThan.
	Sweep(ctx context.Context, olderThan time.Duration)
}

// uploadBackends holds registered non-filesystem upload backends (process
// global), mirroring RegisterBlobBackend.
var uploadBackends = map[string]func(dir string) (UploadStore, error){}

// RegisterUploadBackend installs a constructor for an upload backend name. A
// module (e.g. s3blob) calls this from its init() so consumers enable it with a
// blank import.
func RegisterUploadBackend(name string, build func(dir string) (UploadStore, error)) {
	if name == "" || build == nil {
		return
	}
	uploadBackends[name] = build
}

// OpenUploadStore returns the staging store for a backend name. "filesystem"
// (default) is built in and roots its files at dir; other backends (e.g. "s3")
// register a constructor. Requesting an unregistered backend fails loudly — a
// silent fallback would put multi-GB uploads on the wrong storage.
func OpenUploadStore(backend, dir string) (UploadStore, error) {
	switch backend {
	case "", "filesystem":
		return NewFileUploadStore(dir)
	}
	if build, ok := uploadBackends[backend]; ok {
		return build(dir)
	}
	return nil, fmt.Errorf("unsupported upload backend %q (filesystem, or a registered one)", backend)
}

// FileUploadStore stages uploads as one append-only file per session on the
// local filesystem (the historical OCI behavior). It is the default and is
// used whenever the blob backend is filesystem.
type FileUploadStore struct {
	root string
	mu   sync.Map // session -> *sync.Mutex
}

// NewFileUploadStore roots a FileUploadStore at dir.
func NewFileUploadStore(dir string) (*FileUploadStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileUploadStore{root: dir}, nil
}

func (s *FileUploadStore) lock(session string) *sync.Mutex {
	v, _ := s.mu.LoadOrStore(session, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// partPath is the session's backing file. Session ids are validated to reject
// path traversal (the format is enforced by the OCI adapter: "sess-"+32 hex).
func (s *FileUploadStore) partPath(session string) (string, error) {
	if session == "" || strings.ContainsAny(session, "/\\") || strings.Contains(session, "..") {
		return "", fmt.Errorf("invalid session id")
	}
	return filepath.Join(s.root, session+".part"), nil
}

func (s *FileUploadStore) Append(ctx context.Context, session string, r io.Reader) (int64, error) {
	mu := s.lock(session)
	mu.Lock()
	defer mu.Unlock()
	p, err := s.partPath(session)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (s *FileUploadStore) Size(_ context.Context, session string) int64 {
	p, err := s.partPath(session)
	if err != nil {
		return 0
	}
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (s *FileUploadStore) Open(_ context.Context, session string) (io.ReadCloser, error) {
	p, err := s.partPath(session)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

func (s *FileUploadStore) Delete(_ context.Context, session string) {
	if p, err := s.partPath(session); err == nil {
		_ = os.Remove(p)
	}
	s.mu.Delete(session)
}

func (s *FileUploadStore) Sweep(_ context.Context, olderThan time.Duration) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || fi.IsDir() {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			name := e.Name()
			if len(name) > 5 && filepath.Ext(name) == ".part" {
				_ = os.Remove(filepath.Join(s.root, name))
			}
		}
	}
}
