package git

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"
)

// requestBody reads a request body, transparently decompressing a
// `Content-Encoding: gzip` (or `deflate`) body.
//
// Git's HTTP client compresses larger upload-pack request bodies (it switches
// to gzip above a size threshold), and a normal smart-HTTP deployment relies on
// the web server (git http-backend) to decompress before git sees the bytes.
// artifact drives `git upload-pack --stateless-rpc` directly, so it MUST
// decompress itself: feeding the compressed bytes to upload-pack makes it fail
// with "bad line length character" (exit 128), which the client then reports as
// a protocol error. Small requests are sent uncompressed, which is why only
// big / many-ref repositories used to fail.
func requestBody(r *http.Request, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		return nil, err
	}
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); {
	case enc == "", enc == "identity":
		return raw, nil
	case strings.Contains(enc, "gzip"):
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(io.LimitReader(zr, limit))
	case strings.Contains(enc, "deflate"):
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(io.LimitReader(zr, limit))
	default:
		return raw, nil
	}
}
