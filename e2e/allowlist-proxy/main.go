// allowlist-proxy is a tiny HTTP/HTTPS forward proxy that REFUSES every CONNECT
// and every request whose host is not the mount.
//
// Why this exists: GitHub runners have outbound network. Package managers will
// happily fetch from their real registries (rubygems.org, packagist.org, ...)
// even when they are configured to use a mirror, and then the "test" proves
// nothing - easyblobs sees zero requests. Blanking HTTP(S)_PROXY is not enough
// either: with direct egress the client simply goes straight out.
//
// So the client is given THIS proxy and nothing else:
//   - plain GET to the allowed host  -> forwarded
//   - CONNECT (i.e. TLS to anywhere) -> refused with 403
//   - any other host                 -> refused with 403
//
// Result: the ONLY reachable registry inside a protocol job is the mount.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// relayConnect implements a plain CONNECT tunnel for an allowlisted host. It is
// deliberately minimal: dial, tell the client 200, then splice the two
// connections. Used only for origins listed in ALLOWED_EGRESS.
func relayConnect(w http.ResponseWriter, r *http.Request) {
	upstream, err := net.DialTimeout("tcp", r.Host, 15*time.Second)
	if err != nil {
		log.Printf("CONNECT dial %s: %v", r.Host, err)
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	log.Printf("ALLOW CONNECT %s", r.Host)

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	<-done
}

func main() {
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8888"
	}
	// The only host the client may reach, e.g. "127.0.0.1:8080".
	allowed := os.Getenv("ALLOWED_HOST")
	if allowed == "" {
		log.Fatal("allowlist-proxy: ALLOWED_HOST is required")
	}
	allowed = strings.ToLower(allowed)

	// Some protocols are pure PASSTHROUGH mirrors: the adapter serves no local
	// copy and the client's own manifest/index/objects live upstream (OCI's
	// registry and luarocks' repository are the two clearest cases). Blocking
	// their origin would not prove anything about the adapter - it would only
	// prove the origin is unreachable. Those hosts may be listed explicitly in
	// ALLOWED_EGRESS, as a comma-separated allowlist.
	//
	// Everything else stays blocked, so the mount is still the only way to get
	// a package that the adapter actually caches.
	egress := map[string]bool{}
	for _, h := range strings.Split(os.Getenv("ALLOWED_EGRESS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			egress[h] = true
		}
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CONNECT = the client wants a raw tunnel (normally for TLS). Refusing
		// it is what actually blocks `https://rubygems.org`.
		if r.Method == http.MethodConnect {
			// A CONNECT names host:port; compare on the host part.
			h := strings.ToLower(r.Host)
			if i := strings.LastIndex(h, ":"); i > 0 {
				h = h[:i]
			}
			if egress[h] {
				relayConnect(w, r)
				return
			}
			log.Printf("DENY CONNECT %s", r.Host)
			http.Error(w, "blocked by allowlist-proxy", http.StatusForbidden)
			return
		}
		host := strings.ToLower(r.Host)
		if host != allowed {
			log.Printf("DENY %s %s%s", r.Method, host, r.URL.Path)
			http.Error(w, "blocked by allowlist-proxy", http.StatusForbidden)
			return
		}
		// Forward to the allowed host, preserving method/headers/body.
		out := *r
		out.RequestURI = ""
		out.URL.Scheme = "http"
		out.URL.Host = allowed
		res, err := http.DefaultTransport.RoundTrip(&out)
		if err != nil {
			log.Printf("ERR  %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		for k, vs := range res.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("allowlist-proxy on %s, only %s allowed\n", addr, allowed)
	log.Fatal(srv.Serve(ln))
}
