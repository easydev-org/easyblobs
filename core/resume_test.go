package easyblobkit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// TestResumableBodyResumesOnDrop: the upstream serves the body but truncates it
// mid-stream on the first attempt; the resumable reader must recover the rest
// with a Range request so the caller sees the COMPLETE object.
func TestResumableBodyResumesOnDrop(t *testing.T) {
	full := bytes.Repeat([]byte("abcdefghij"), 1000) // 10000 bytes
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		if r.Header.Get("Range") == "" {
			// First attempt: advertise the full size but send only half, then
			// abort the connection (the reported mid-stream drop).
			w.Header().Set("Content-Length", strconv.Itoa(len(full)))
			_, _ = w.Write(full[:5000])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			panic(http.ErrAbortHandler) // sever the connection
		}
		// Resume: honour the Range.
		start := 5000
		if spec := strings.TrimPrefix(r.Header.Get("Range"), "bytes="); spec != "" {
			if i := strings.IndexByte(spec, '-'); i > 0 {
				if n, err := strconv.Atoi(spec[:i]); err == nil {
					start = n
				}
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(full)-start))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(full[start:])
		_ = call
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{}
	body, err := openResumable(context.Background(), client, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read: %v (got %d bytes)", err, len(got))
	}
	if !bytes.Equal(got, full) {
		t.Fatalf("resumed body mismatch: got %d bytes, want %d", len(got), len(full))
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected a resume (>=2 upstream calls), got %d", calls)
	}
}

// TestResumableBodyCleanEOF: a normal, complete transfer returns io.EOF without
// any extra request.
func TestResumableBodyCleanEOF(t *testing.T) {
	full := []byte("hello world")
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write(full)
	}))
	t.Cleanup(srv.Close)

	body, err := openResumable(context.Background(), &http.Client{}, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("got %q err=%v", got, err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("expected exactly 1 upstream call, got %d", n)
	}
}

var _ = fmt.Sprintf
