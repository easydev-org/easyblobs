package hex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

func newHandler(t *testing.T) http.Handler {
	t.Helper()
	dir := t.TempDir()
	meta, err := store.OpenSQLite(dir + "/m.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	blobs, err := store.NewFileBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	reg := &easyblobkit.Registry{
		Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta),
		Upstreams: &easyblobkit.Upstreams{Defaults: map[string]string{}, Overrides: map[string]string{}, Proxy: map[string]string{}},
	}
	h, err := NewHandler(reg, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestErlangTermEncoding pins the ETF wire format Hex clients decode. The
// encoder is the CONTRACT: these are the exact tags an Erlang/Elixir client
// reads, so a silent change corrupts every published package's metadata.
// (The package only WRITES ETF, so we assert tags/bytes, not a round trip.)
//
// Read from erlangterm.go: strings use BINARY_EXT(109) + u32 len + 0 flags;
// ints use INTEGER_EXT(98) + 4 bytes; atoms use ATOM_UTF8_EXT(118) + u16 len;
// maps use MAP_EXT(116) + u32 arity; nil/empty list is NIL_EXT(106). The
// 131 version byte is added by termToBinary, NOT by encodeTerm.
func TestErlangTermEncoding(t *testing.T) {
	// termToBinary prepends the ETF version tag 131.
	if b := termToBinary("x"); len(b) == 0 || b[0] != 131 {
		t.Fatalf("termToBinary must prepend 131, got %v", b)
	}
	// encodeTerm alone does NOT include 131 (it appends to a caller's buffer).
	raw := encodeTerm(nil, "hi")
	if len(raw) == 0 || raw[0] == 131 {
		t.Fatalf("encodeTerm must not add the version byte, got %v", raw)
	}
	// BINARY_EXT(109), then a 4-byte big-endian length = 5, flags 0, payload.
	bin := encodeTerm(nil, "hello")
	if bin[0] != 109 {
		t.Fatalf("string must use BINARY_EXT(109), got %d", bin[0])
	}
	if got := int(bin[1])<<24 | int(bin[2])<<16 | int(bin[3])<<8 | int(bin[4]); got != 5 {
		t.Fatalf("BINARY_EXT length = %d, want 5", got)
	}
	if bin[5] != 0 {
		t.Fatalf("BINARY_EXT flags = %d, want 0", bin[5])
	}
	if string(bin[6:]) != "hello" {
		t.Fatalf("payload = %q", string(bin[6:]))
	}
	// INTEGER_EXT(98) + 4 big-endian bytes.
	n := encodeTerm(nil, 42)
	if len(n) != 5 || n[0] != 98 || n[4] != 42 {
		t.Fatalf("int 42 must be INTEGER_EXT(98)+4B, got %v", n)
	}
	// ATOM_UTF8_EXT(118) + u16 length.
	a := encodeTerm(nil, true)
	if a[0] != 118 {
		t.Fatalf("bool must encode as an atom(118), got %d", a[0])
	}
	if got := int(a[1])<<8 | int(a[2]); string(a[3:3+got]) != "true" {
		t.Fatalf("atom = %q, want true", string(a[3:3+got]))
	}
	// nil -> NIL_EXT(106).
	if b := encodeTerm(nil, nil); len(b) != 1 || b[0] != 106 {
		t.Fatalf("nil must be NIL_EXT(106), got %v", b)
	}
	// map -> MAP_EXT(116) + u32 arity.
	m := encodeTerm(nil, map[string]any{"a": "b"})
	if m[0] != 116 {
		t.Fatalf("map must be MAP_EXT(116), got %d", m[0])
	}
	if got := int(m[1])<<24 | int(m[2])<<16 | int(m[3])<<8 | int(m[4]); got != 1 {
		t.Fatalf("map arity = %d, want 1", got)
	}
}

// TestUnknownRouteDoesNotPanic: an unknown path must answer, not crash.
func TestUnknownRouteDoesNotPanic(t *testing.T) {
	h := newHandler(t)
	if w := do(h, http.MethodGet, "/artifacts/hex/does/not/exist", ""); w.Code == http.StatusOK {
		t.Fatalf("unknown route must not be 200: %s", w.Body.String())
	}
}
