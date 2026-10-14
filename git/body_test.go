package git

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mkReq(t *testing.T, body []byte, encoding string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/git-upload-pack", bytes.NewReader(body))
	if encoding != "" {
		r.Header.Set("Content-Encoding", encoding)
	}
	return r
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func zl(b []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

// TestRequestBodyDecompresses locks the fix for the git mirror bug: a
// Content-Encoding: gzip upload-pack body must be decompressed before it is fed
// to `git upload-pack` (git compresses larger requests; a plain HTTP server
// does this, artifact must do it itself).
func TestRequestBodyDecompresses(t *testing.T) {
	payload := []byte("0032want 00000111bca880e79987bdf3da54be4585867624\n00000009done\n")

	cases := []struct {
		name string
		body []byte
		enc  string
	}{
		{"plain", payload, ""},
		{"identity", payload, "identity"},
		{"gzip", gz(payload), "gzip"},
		{"deflate", zl(payload), "deflate"},
	}
	for _, c := range cases {
		got, err := requestBody(mkReq(t, c.body, c.enc), 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("%s: got %q want %q", c.name, got, payload)
		}
	}
}

// TestRequestBodyGzipRoundTrip proves a gzipped body that would have failed
// (compressed bytes fed to upload-pack) now yields the original request.
func TestRequestBodyGzipRoundTrip(t *testing.T) {
	big := bytes.Repeat([]byte("0032want 00000111bca880e79987bdf3da54be4585867624\n"), 500)
	r := mkReq(t, gz(big), "gzip")
	got, err := requestBody(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("gzip round-trip mismatch")
	}
	_ = io.Discard
}
