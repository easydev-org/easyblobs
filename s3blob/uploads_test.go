package s3blob

import (
	"io"
	"os"
	"runtime"
	"testing"
)

// chunkReader yields n bytes without allocating them all.
type chunkReader struct{ left int }

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > c.left {
		n = c.left
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	c.left -= n
	return n, nil
}

// TestSpoolToTempDoesNotBufferInMemory guards the OOM fix: a large chunk must be
// spooled to a temp file, not read into a single in-memory buffer. The old
// io.ReadAll path grew the heap by ~the whole chunk; the fix must not.
func TestSpoolToTempDoesNotBufferInMemory(t *testing.T) {
	const size = 256 << 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	f, n, err := spoolToTemp(&chunkReader{left: size})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()

	runtime.ReadMemStats(&after)
	if n != size {
		t.Fatalf("spooled %d bytes, want %d", n, size)
	}
	grew := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if grew > size/4 {
		t.Fatalf("heap grew by %d bytes for a %d-byte chunk — it was buffered in memory", grew, size)
	}
	// The file must be rewound so the caller can read it back.
	buf := make([]byte, 16)
	if _, err := f.Read(buf); err != nil {
		t.Fatalf("rewound file not readable: %v", err)
	}
}
