package hex

import (
	"bytes"
	"compress/gzip"
	"net/http"

	"github.com/easydev-org/easyblobs/core"
)

func (s *State) publicKey(w http.ResponseWriter, r *http.Request) {
	easyblobkit.Text(w, http.StatusOK, staticPublicKey, "application/x-pem-file")
}

func (s *State) names(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "hex")
	payload := pbStr(nil, 1, "registry")
	for _, name := range repos {
		pkg := pbStr(nil, 1, name)
		ts := pbInt(pbInt(nil, 1, timeNow()), 2, 0)
		pkg = pbBytes(pkg, 2, ts)
		payload = pbBytes(payload, 2, pkg)
	}
	s.signedGzip(w, r, payload)
}

func (s *State) versions(w http.ResponseWriter, r *http.Request) {
	repos, _ := s.Registry.Meta.ListRepositoriesByFormat(r.Context(), "hex")
	payload := pbStr(nil, 1, "registry")
	for _, name := range repos {
		vs, _ := s.Registry.Meta.ListVersions(r.Context(), "hex", name)
		pkg := pbStr(nil, 1, name)
		for _, v := range vs {
			pkg = pbStr(pkg, 2, v)
		}
		payload = pbBytes(payload, 2, pkg)
	}
	s.signedGzip(w, r, payload)
}

// signedGzip signs the payload and wraps it in the Hex registry envelope.
func (s *State) signedGzip(w http.ResponseWriter, r *http.Request, payload []byte) {
	// Sign the payload bytes themselves, then wrap as the Hex registry
	// envelope: field1 payload, field2 signature. The signature is an RSA
	// SHA-256 (PKCS#1 v1.5) over the payload with the static key whose public
	// half /public_key serves — the mix client verifies it before unpacking,
	// so an empty signature fails with "signature verification failed".
	sig, err := signPayload(payload)
	if err != nil {
		sig = nil
	}
	var env []byte
	env = pbBytes(env, 1, payload)
	env = pbBytes(env, 2, sig)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(env)
	_ = zw.Close()
	easyblobkit.OctetResponse(w, r, buf.Bytes())
}

func timeNow() int { return 946684800 }

// pbRetirement builds a default (non-retired) Retirement record, matching the
// hex.pm wire shape: field1 retired_at (varint), field2 reason (varint).
func pbRetirement() []byte {
	// Retirement{f1: 0, f2: 0} -> 0x08 0x00 0x10 0x00
	return []byte{0x08, 0x00, 0x10, 0x00}
}

func bytesHasNonZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return true
		}
	}
	return false
}

// --- minimal protobuf wire helpers ------------------------------------------

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbTag(b []byte, field int, wire int) []byte {
	return pbVarint(b, uint64(field)<<3|uint64(wire))
}

func pbBytes(b []byte, field int, data []byte) []byte {
	b = pbTag(b, field, 2)
	b = pbVarint(b, uint64(len(data)))
	return append(b, data...)
}

func pbStr(b []byte, field int, s string) []byte {
	return pbBytes(b, field, []byte(s))
}

func pbInt(b []byte, field, v int) []byte {
	b = pbTag(b, field, 0)
	return pbVarint(b, uint64(v))
}

const staticPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA222P0sX1fAdDTKwaCALP
c6m3X9m8LhUFoRAoDWinRUjPsZyIFtJ18EEasu4GeOltuZvO+fDf5qkMtRmuj638
PFXKlb/YCrtdfhNCIup+EuwUxJJVHC2Uj0D48x+iQ7/QQZ2cdRZ3AReN+3SDeFlj
nZDq0Qy9HEDJy/HFPm/KEGWD0Rxem9Bobwvz+6wsFUL9Zb28LBkygtCA9FJDi1su
QSj9NamlgxEd+nOdn/6LuVSnRBSc8UCEX4EfGZIIpys/MePCA2DdlFprH8kuFqpY
ynLTPvKdVLbp/eJEgnbBLwSL2SwkH8iUxUDedeQs0pxtVhn/kntpTGk/1lbk3y9D
LQIDAQAB
-----END PUBLIC KEY-----`
