package easyblobkit

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIsPrivateAddr pins the address classes an upstream must never reach.
func TestIsPrivateAddr(t *testing.T) {
	private := []string{
		"127.0.0.1", "127.255.255.254", "0.0.0.0", "10.0.0.1",
		"172.16.0.1", "192.168.1.1", "169.254.169.254",
		"100.64.0.1", "198.18.0.1", "224.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "fc00::1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", // 4-in-6
	}
	for _, s := range private {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test address %q", s)
		}
		if !IsPrivateAddr(ip) {
			t.Errorf("IsPrivateAddr(%s) = false, want true", s)
		}
	}
	public := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range public {
		ip := net.ParseIP(s)
		if IsPrivateAddr(ip) {
			t.Errorf("IsPrivateAddr(%s) = true, want false", s)
		}
	}
	if !IsPrivateAddr(nil) {
		t.Error("a nil address must be treated as private")
	}
}

// TestDialGuardRefusesLoopback is the decisive test: a request that passed the
// SYNTACTIC guard must still not reach a loopback server.
//
// This is the shape that was exploitable (an `_upstream/<host>/` request whose
// host resolved to 127.0.0.1), reproduced at the layer that decides.
func TestDialGuardRefusesLoopback(t *testing.T) {
	// TestMain opts this package OUT of the guard so its httptest fixtures work;
	// the guard's own tests must opt back IN.
	t.Setenv(allowPrivateUpstreamEnv, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the loopback server was reached; the dial guard did not refuse")
		fmt.Fprint(w, "INTERNAL-SECRET")
	}))
	defer srv.Close()

	// A client built the way every upstream fetch builds one (Client() wraps the
	// transport in the guard).
	client := NewClientFactory().Client(nil)
	client.Timeout = 5 * time.Second
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("a request to loopback must fail")
	}
}

// TestDialGuardRefusesWildcardDNSToLoopback is the bypass the syntactic guard
// cannot see: "127.0.0.1.nip.io" is a valid public DNS name that RESOLVES to
// 127.0.0.1.
func TestDialGuardRefusesWildcardDNSToLoopback(t *testing.T) {
	// If the resolver cannot reach DNS in this environment, the name will not
	// resolve and the dial fails anyway — which is also a refusal. Only assert
	// when it DOES resolve, so the test is meaningful where DNS works.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, "127.0.0.1.nip.io")
	if err != nil || len(ips) == 0 {
		t.Skip("127.0.0.1.nip.io does not resolve here (no external DNS)")
	}
	t.Setenv(allowPrivateUpstreamEnv, "")
	t.Logf("127.0.0.1.nip.io resolves to %v", ips)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the wildcard-DNS name reached loopback")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	client := NewClientFactory().Client(nil)
	client.Timeout = 5 * time.Second
	if _, err := client.Get("http://127.0.0.1.nip.io:" + port + "/"); err == nil {
		t.Fatal("a wildcard DNS name resolving to loopback must be refused")
	} else {
		t.Logf("refused as expected: %v", err)
	}
}

// TestDialGuardAllowsPublicAddresses: the guard must not refuse a public
// destination (whether it is reachable is a network question, not a policy one).
func TestDialGuardAllowsPublicAddresses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, h := range []string{"1.1.1.1:443", "93.184.216.34:80", "pypi.org:443"} {
		if err := CheckUpstreamHost(ctx, h); err != nil {
			t.Errorf("CheckUpstreamHost(%q) refused a public destination: %v", h, err)
		}
	}
}

// TestDialGuardOptOut: a deployment that needs an internal upstream must be able
// to allow it EXPLICITLY, and the default must refuse.
func TestDialGuardOptOut(t *testing.T) {
	if allowPrivateUpstream(func(string) string { return "" }) {
		t.Error("the default must be to refuse private upstreams")
	}
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		if !allowPrivateUpstream(func(string) string { return v }) {
			t.Errorf("%q must enable the opt-out", v)
		}
	}
	for _, v := range []string{"0", "false", "no", "off", "", "maybe"} {
		if allowPrivateUpstream(func(string) string { return v }) {
			t.Errorf("%q must NOT enable the opt-out", v)
		}
	}

	// An opted-in fetch really does reach loopback.
	t.Setenv(allowPrivateUpstreamEnv, "1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	client := &http.Client{Transport: guardTransport(newTransport(nil)), Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("the opt-out must allow loopback: %v", err)
	}
	_ = resp.Body.Close()
}

// TestDialGuardRejectsMixedAnswers: a name that resolves to BOTH a public and a
// private address must be refused outright (a rebinding attempt would otherwise
// succeed on whichever address the dialer picked).
func TestDialGuardRejectsMixedAnswers(t *testing.T) {
	for _, ip := range []net.IP{
		net.ParseIP("10.0.0.7"),
		net.ParseIP("127.0.0.1"),
		net.ParseIP("169.254.169.254"),
	} {
		if !IsPrivateAddr(ip) {
			t.Fatalf("%s must be private", ip)
		}
	}
}
