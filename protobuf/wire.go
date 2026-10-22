package protobuf

// Minimal protobuf wire-format support.
//
// buf's Connect client sends application/proto (BINARY), not JSON - visible in
// the server log:
//
//   req POST /buf.registry.module.v1.ModuleService/GetModules
//       ct="application/proto" -> 404
//
// Pulling in google.golang.org/protobuf plus generated code for the whole BSR
// API would add a large dependency for a handful of messages, so the small
// subset this adapter needs is decoded directly from the wire format.
//
// The format is a sequence of (key, value) pairs where key = (field<<3)|wiretype:
//
//	0 varint, 1 fixed64, 2 length-delimited (string/bytes/message), 5 fixed32
//
// Only the length-delimited and varint forms appear in the messages used here.

import (
	"errors"
	"fmt"
)

// printableBody renders a request body for the debug log: text stays readable,
// non-printable bytes become \xNN. A plain %q on binary produced escapes that hid
// the structure, which is how a JSON body once looked like protobuf.
func printableBody(b []byte) string {
	const max = 512
	truncated := false
	if len(b) > max {
		b = b[:max]
		truncated = true
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c < 0x7f || c == '\n' || c == '\t' {
			out = append(out, c)
		} else {
			out = append(out, []byte(fmt.Sprintf("\\x%02x", c))...)
		}
	}
	s := string(out)
	if truncated {
		s += "...(truncated)"
	}
	return s
}

// wireField is one decoded field occurrence.
type wireField struct {
	Num  int      // field number
	Wire int      // wire type
	Bytes []byte  // wire type 2
	Var  uint64   // wire type 0
}

// wireDecode splits a message into its fields. Unknown fields are returned too,
// which is what lets a decoder skip what it does not understand - the same
// behaviour a generated parser has.
func wireDecode(b []byte) ([]wireField, error) {
	var out []wireField
	for len(b) > 0 {
		key, n := wireVarint(b)
		if n <= 0 {
			return nil, errors.New("protobuf: truncated field key")
		}
		b = b[n:]
		num, typ := int(key>>3), int(key&0x7)
		f := wireField{Num: num, Wire: typ}
		switch typ {
		case 0: // varint
			v, m := wireVarint(b)
			if m <= 0 {
				return nil, errors.New("protobuf: truncated varint")
			}
			f.Var = v
			b = b[m:]
		case 1: // fixed64
			if len(b) < 8 {
				return nil, errors.New("protobuf: truncated fixed64")
			}
			b = b[8:]
		case 2: // length-delimited
			l, m := wireVarint(b)
			if m <= 0 {
				return nil, errors.New("protobuf: truncated length")
			}
			b = b[m:]
			if uint64(len(b)) < l {
				return nil, fmt.Errorf("protobuf: field %d wants %d bytes, have %d", num, l, len(b))
			}
			f.Bytes = b[:l]
			b = b[l:]
		case 5: // fixed32
			if len(b) < 4 {
				return nil, errors.New("protobuf: truncated fixed32")
			}
			b = b[4:]
		default:
			return nil, fmt.Errorf("protobuf: unsupported wire type %d (field %d)", typ, num)
		}
		out = append(out, f)
	}
	return out, nil
}

// wireVarint reads a base-128 varint.
func wireVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, -1
}

// wireAppendVarint appends v as a varint (used by the encoder below).
func wireAppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// wireAppendString appends a length-delimited string field.
func wireAppendString(dst []byte, num int, s string) []byte {
	if s == "" {
		return dst // proto3 implicit presence: empty fields are omitted
	}
	dst = wireAppendVarint(dst, uint64(num)<<3|2)
	dst = wireAppendVarint(dst, uint64(len(s)))
	return append(dst, s...)
}

// wireAppendBytes appends a length-delimited bytes field.
func wireAppendBytes(dst []byte, num int, b []byte) []byte {
	if len(b) == 0 {
		return dst
	}
	dst = wireAppendVarint(dst, uint64(num)<<3|2)
	dst = wireAppendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

// wireAppendMessage appends an embedded message field.
func wireAppendMessage(dst []byte, num int, msg []byte) []byte {
	if len(msg) == 0 {
		return dst
	}
	dst = wireAppendVarint(dst, uint64(num)<<3|2)
	dst = wireAppendVarint(dst, uint64(len(msg)))
	return append(dst, msg...)
}

// wireAppendVarintField appends a varint field.
func wireAppendVarintField(dst []byte, num int, v uint64) []byte {
	if v == 0 {
		return dst
	}
	dst = wireAppendVarint(dst, uint64(num)<<3|0)
	return wireAppendVarint(dst, v)
}

// firstBytes returns the first length-delimited value of a field number.
func firstBytes(fields []wireField, num int) []byte {
	for _, f := range fields {
		if f.Num == num && f.Wire == 2 {
			return f.Bytes
		}
	}
	return nil
}

// firstString returns the first length-delimited value of a field as a string.
func firstString(fields []wireField, num int) string {
	return string(firstBytes(fields, num))
}

// allBytes returns every length-delimited value of a field number.
func allBytes(fields []wireField, num int) [][]byte {
	var out [][]byte
	for _, f := range fields {
		if f.Num == num && f.Wire == 2 {
			out = append(out, f.Bytes)
		}
	}
	return out
}
