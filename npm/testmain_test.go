package npm

import (
	"os"
	"testing"
)

// TestMain allows the suites in this package to talk to the httptest servers
// they start on 127.0.0.1.
//
// The upstream guard refuses a PRIVATE destination by default, which is the
// point: a request-driven fetch must not reach the cluster it runs in. A test
// that stands up its own local server is the one case that legitimately needs
// the opt-out, and it must be EXPLICIT — the same way a deployment that mirrors
// an internal upstream has to say so.
func TestMain(m *testing.M) {
	if os.Getenv("EASYBLOBS_ALLOW_PRIVATE_UPSTREAM") == "" {
		os.Setenv("EASYBLOBS_ALLOW_PRIVATE_UPSTREAM", "1")
	}
	os.Exit(m.Run())
}
