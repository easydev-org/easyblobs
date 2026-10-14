package easyblobkit

import "errors"

// ErrProtocolUnknown is returned when cmd-artifact asks for a protocol that was not
// registered (i.e. no adapter package was blank-imported).
func ErrProtocolUnknown(name string) error {
	return errors.New("easyblobkit: unknown protocol: " + name)
}

// ErrDigestMismatch is the sentinel a BlobStore returns from Put when the
// content does not hash to the caller-declared digest. Callers map it onto a
// client-facing "digest does not match content" (400) rather than a 500.
var ErrDigestMismatch = errors.New("digest does not match content")
