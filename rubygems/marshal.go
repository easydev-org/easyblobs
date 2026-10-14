package rubygems

// --- Ruby Marshal 4.8 encoders (byte-exact port of the reference) ----------

func marshalLen(b []byte, n int) []byte {
	if n <= 122 {
		return append(b, byte(n+5))
	}
	var mag []byte
	v := n
	for v > 0 {
		mag = append(mag, byte(v&0xff))
		v >>= 8
	}
	b = append(b, byte(len(mag)))
	return append(b, mag...)
}

func marshalArrayLen(b []byte, n int) []byte {
	out := append(b, 0x5b)
	return marshalLen(out, n)
}

// marshalIstringFirst emits the :E encoding ivar inline (the FIRST string).
func marshalIstringFirst(b []byte, s string) []byte {
	out := append(b, 0x49, 0x22)
	out = marshalLen(out, len(s))
	out = append(out, s...)
	return append(out, 0x06, 0x3a, 0x06, 0x45, 0x54) // :E inline
}

// marshalIstring emits a symbol link (index 0) — subsequent strings.
func marshalIstring(b []byte, s string) []byte {
	out := append(b, 0x49, 0x22)
	out = marshalLen(out, len(s))
	out = append(out, s...)
	return append(out, 0x06, 0x3b, 0x00, 0x54) // :E symbol link 0
}

// marshalVersion wraps a version string in a Gem::Version user-marshal.
func marshalVersion(b []byte, version string) []byte {
	out := append(b, 0x55, 0x3a, 0x11)
	out = append(out, []byte("Gem::Version")...)
	out = append(out, 0x5b, 0x06)
	return marshalIstring(out, version)
}

// marshalRequirement wraps Gem::Requirement user-marshal [">=", Gem::Version("0")].
func marshalRequirement(b []byte) []byte {
	out := append(b, 0x55, 0x3a, 0x15)
	out = append(out, []byte("Gem::Requirement")...)
	out = append(out, 0x5b, 0x06, 0x5b, 0x06, 0x5b, 0x07)
	out = marshalIstring(out, ">=")
	return marshalVersion(out, "0")
}

// marshalFixnum encodes a Ruby fixnum.
func marshalFixnum(b []byte, n int) []byte {
	out := append(b, 0x69)
	if n >= 0 && n <= 122 {
		out = append(out, byte(n+5))
		return out
	}
	var mag []byte
	v := n
	for v > 0 {
		mag = append(mag, byte(v&0xff))
		v >>= 8
	}
	out = append(out, byte(len(mag)))
	return append(out, mag...)
}

// marshalTime encodes a fixed UTC epoch Time ('I' 'u' :Time ... + ivar zone UTC).
func marshalTime(b []byte) []byte {
	out := append(b, 0x49, 0x75, 0x3a, 0x09)
	out = append(out, []byte("Time")...)
	out = append(out, 0x0d, 0x40, 0x00, 0x14, 0xc0, 0x00, 0x00, 0x00, 0x00)
	out = append(out, 0x06, 0x3a, 0x09)
	out = append(out, []byte("zone")...)
	return marshalIstring(out, "UTC")
}

func marshalAuthors(b []byte) []byte {
	out := append(b, 0x5b, 0x06)
	return marshalIstring(out, "")
}

func marshalLicenses(b []byte) []byte {
	out := append(b, 0x5b, 0x06)
	return marshalIstring(out, "MIT")
}

func marshalSpecs(tuples [][2]string) []byte {
	b := []byte{0x04, 0x08}
	b = marshalArrayLen(b, len(tuples))
	for i, t := range tuples {
		b = marshalArrayLen(b, 3)
		if i == 0 {
			b = marshalIstringFirst(b, t[0])
		} else {
			b = marshalIstring(b, t[0])
		}
		b = marshalVersion(b, t[1])
		if i == 0 {
			b = marshalIstringFirst(b, "ruby")
		} else {
			b = marshalIstring(b, "ruby")
		}
	}
	return b
}

// marshalSpecArray builds the 19-element spec array (spec format 4) exactly
// as Ruby's Gem::Specification#_dump expects.
func marshalSpecArray(name, version string) []byte {
	b := []byte{0x04, 0x08}
	b = marshalArrayLen(b, 19)
	b = marshalIstringFirst(b, "4.0.6") // 0 rubygems_version
	b = marshalFixnum(b, 4)             // 1 specification_version
	b = marshalIstring(b, name)         // 2 name
	b = marshalVersion(b, version)      // 3 version
	b = marshalTime(b)                  // 4 date
	b = marshalIstring(b, "")           // 5 summary
	b = marshalRequirement(b)           // 6 required_ruby_version
	b = marshalRequirement(b)           // 7 required_rubygems_version
	b = append(b, 0x30)                 // 8 original_platform ("ruby" symbol ref)
	b = marshalArrayLen(b, 0)           // 9 dependencies []
	b = append(b, 0x30)                 // 10 rubyforge_project
	b = append(b, 0x30)                 // 11 email
	b = marshalAuthors(b)               // 12 authors
	b = append(b, 0x30)                 // 13 description
	b = append(b, 0x30)                 // 14 homepage
	b = append(b, 0x54)                 // 15 has_rdoc true
	b = marshalIstring(b, "ruby")       // 16 new_platform
	b = marshalLicenses(b)              // 17 licenses
	b = append(b, 0x7b, 0x00)           // 18 metadata: empty hash
	return b
}

// marshalSpecification wraps the spec array in a 'u' user-marshal with the
// w_long byte-count length prefix.
func marshalSpecification(name, version string) []byte {
	payload := marshalSpecArray(name, version)
	b := []byte{0x04, 0x08}
	b = append(b, 0x75, 0x3a, 0x17) // 'u' :Gem::Specification
	b = append(b, []byte("Gem::Specification")...)
	mag := []byte{}
	v := len(payload)
	for v > 0 {
		mag = append(mag, byte(v&0xff))
		v >>= 8
	}
	b = append(b, byte(len(mag)))
	b = append(b, mag...)
	b = append(b, payload...)
	return b
}
