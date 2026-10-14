package oci

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// scopedRegistry builds a registry whose IndexStore is the production
// scoped decorator, so the tests exercise the real namespace/key interaction.
func scopedRegistry(t *testing.T) *easyblobkit.Registry {
	t.Helper()
	blobs, err := store.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.OpenSQLite(t.TempDir() + "/meta.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = meta.Close() })
	return &easyblobkit.Registry{
		Blobs: blobs, Meta: easyblobkit.NewScopedStore(meta),
		Upstreams: &easyblobkit.Upstreams{Defaults: map[string]string{"oci": "https://registry-1.docker.io"}, AirGap: true},
	}
}

// catalogServer serves the OCI adapter behind the production scope middleware,
// so a request's Host selects the repository namespace exactly as in a real
// deployment. auth, when non-nil, gates the catalog.
func catalogServer(t *testing.T, reg *easyblobkit.Registry, auth easyblobkit.Auth) *httptest.Server {
	t.Helper()
	a := New(&OciState{Registry: reg, SelfBase: "http://registry.test", Auth: auth})
	h := easyblobkit.ScopeMiddlewareForFormat("oci", "/v2", a)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// pushImage pushes a one-layer manifest to <name>:<tag> through the real /v2
// API, with the given Host (the registry the client dialed).
func pushImage(t *testing.T, srv *httptest.Server, host, name, tag string) {
	t.Helper()
	layer := []byte("layer-for-" + name + ":" + tag)
	digest := easyblobkit.DigestOf(layer)

	u := srv.URL + "/v2/" + name + "/blobs/uploads/?digest=" + digest
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(string(layer)))
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("blob upload: %d", resp.StatusCode)
	}

	man := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + digest + `","size":` + itoa(len(layer)) + `},` +
		`"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"` + digest + `","size":` + itoa(len(layer)) + `}]}`
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v2/"+name+"/manifests/"+tag, strings.NewReader(man))
	req.Host = host
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("manifest put: %d", resp.StatusCode)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// getJSON issues GET target with Host and optional Basic creds, returning
// (status, decoded body).
func getJSON(t *testing.T, srv *httptest.Server, host, target, user, pass string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+target, nil)
	req.Host = host
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]any{}
	if resp.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(resp.Body).Decode(&out)
	}
	return resp.StatusCode, out
}

func strSlice(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

// TestCatalogEndToEnd drives the real /v2 push path and then the catalog (open
// instance, no auth): the deployment host prefix must not leak, owner queries
// must resolve, and digest alias rows must not appear as tags.
func TestCatalogEndToEnd(t *testing.T) {
	reg := scopedRegistry(t)
	srv := catalogServer(t, reg, nil)

	const host = "registry.test"
	pushImage(t, srv, host, "acme/app", "v1")
	pushImage(t, srv, host, "acme/app", "v2")
	pushImage(t, srv, host, "beta/svc", "v3")

	code, body := getJSON(t, srv, host, "/v2/_catalog", "", "")
	if code != 200 {
		t.Fatalf("catalog code=%d", code)
	}
	got := strSlice(body["repositories"])
	if len(got) != 2 || got[0] != "acme/app" || got[1] != "beta/svc" {
		t.Fatalf("repositories=%v want [acme/app beta/svc]", got)
	}

	code, body = getJSON(t, srv, host, "/v2/_catalog?owner=acme", "", "")
	if code != 200 {
		t.Fatalf("owner code=%d", code)
	}
	images, _ := body["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images=%v", body["images"])
	}
	img, _ := images[0].(map[string]any)
	if img["owner"] != "acme" || img["name"] != "app" {
		t.Fatalf("image=%v", img)
	}
	if tags := strSlice(img["tags"]); len(tags) != 2 || tags[0] != "v1" || tags[1] != "v2" {
		t.Fatalf("tags=%v (want [v1 v2], no sha256 rows)", tags)
	}

	code, body = getJSON(t, srv, host, "/v2/_catalog?owner=acme&name=app", "", "")
	if code != 200 || body["owner"] != "acme" || body["name"] != "app" {
		t.Fatalf("owner+name: code=%d body=%v", code, body)
	}
	if tags := strSlice(body["tags"]); len(tags) != 2 {
		t.Fatalf("tags=%v", tags)
	}

	if code, _ := getJSON(t, srv, host, "/v2/_catalog?owner=acme&name=nope", "", ""); code != 404 {
		t.Fatalf("unknown name code=%d want 404", code)
	}
	if code, body := getJSON(t, srv, host, "/v2/_catalog?owner=nobody", "", ""); code != 200 || len(body["images"].([]any)) != 0 {
		t.Fatalf("unknown owner: code=%d body=%v", code, body)
	}
}

// TestCatalogNoHostPrefixStorage covers a repository stored WITHOUT a
// deployment-host prefix (an explicit upstream host the client named).
func TestCatalogNoHostPrefixStorage(t *testing.T) {
	reg := scopedRegistry(t)
	srv := catalogServer(t, reg, nil)

	pushImage(t, srv, "docker.io", "library/alpine", "latest")

	_, body := getJSON(t, srv, "registry.test", "/v2/_catalog", "", "")
	got := strSlice(body["repositories"])
	if len(got) != 1 || got[0] != "docker.io/library/alpine" {
		t.Fatalf("repositories=%v", got)
	}

	code, body := getJSON(t, srv, "registry.test", "/v2/_catalog?owner=docker.io&name=library/alpine", "", "")
	if code != 200 || body["owner"] != "docker.io" || body["name"] != "library/alpine" {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if tags := strSlice(body["tags"]); len(tags) != 1 || tags[0] != "latest" {
		t.Fatalf("tags=%v", tags)
	}
}

// TestCatalogAuthGated locks the access policy: with auth enabled, _catalog
// requires ANY valid credential (read or write), while pull (manifests) and
// tags/list stay anonymous.
func TestCatalogAuthGated(t *testing.T) {
	reg := scopedRegistry(t)
	auth := easyblobkit.NewTokenAuth("writetok=write,readtok=read")
	srv := catalogServer(t, reg, auth)

	const host = "registry.test"
	// Push needs a write token (auth is on).
	layer := []byte("layer")
	digest := easyblobkit.DigestOf(layer)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v2/acme/app/blobs/uploads/?digest="+digest, strings.NewReader(string(layer)))
	req.Host = host
	req.SetBasicAuth("u", "writetok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("blob upload: %d", resp.StatusCode)
	}
	man := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + digest + `","size":5},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":"` + digest + `","size":5}]}`
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v2/acme/app/manifests/v1", strings.NewReader(man))
	req.Host = host
	req.SetBasicAuth("u", "writetok")
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("manifest put: %d", resp.StatusCode)
	}

	// _catalog anonymous -> 401 with a Bearer challenge.
	code, _ := getJSON(t, srv, host, "/v2/_catalog", "", "")
	if code != 401 {
		t.Fatalf("anonymous catalog code=%d want 401", code)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/v2/_catalog", nil)
	req.Host = host
	resp, _ = http.DefaultClient.Do(req)
	ch := resp.Header.Get("WWW-Authenticate")
	_ = resp.Body.Close()
	if !strings.Contains(ch, "Bearer") || !strings.Contains(ch, "/token") {
		t.Fatalf("challenge=%q", ch)
	}

	// read token -> 200; write token -> 200; owner queries also gated.
	if code, _ := getJSON(t, srv, host, "/v2/_catalog", "u", "readtok"); code != 200 {
		t.Fatalf("read-token catalog code=%d want 200", code)
	}
	if code, _ := getJSON(t, srv, host, "/v2/_catalog", "u", "writetok"); code != 200 {
		t.Fatalf("write-token catalog code=%d want 200", code)
	}
	if code, _ := getJSON(t, srv, host, "/v2/_catalog?owner=acme", "u", "readtok"); code != 200 {
		t.Fatalf("read-token owner query code=%d want 200", code)
	}
	if code, _ := getJSON(t, srv, host, "/v2/_catalog?owner=acme", "", ""); code != 401 {
		t.Fatalf("anonymous owner query code=%d want 401", code)
	}

	// Pull stays anonymous: manifest GET and tags/list -> 200 with no creds.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/v2/acme/app/manifests/v1", nil)
	req.Host = host
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("anonymous manifest pull code=%d want 200", resp.StatusCode)
	}
	if code, _ := getJSON(t, srv, host, "/v2/acme/app/tags/list", "", ""); code != 200 {
		t.Fatalf("anonymous tags/list code=%d want 200", code)
	}
}

// TestStripOwnHost unit-tests the prefix stripping directly.
func TestStripOwnHost(t *testing.T) {
	own := []string{"registry.test", "10.0.0.1:5000"}
	cases := map[string]string{
		"registry.test/acme/app":   "acme/app",
		"10.0.0.1:5000/acme/app":   "acme/app",
		"acme/app":                 "acme/app",
		"docker.io/library/alpine": "docker.io/library/alpine",
		"registry.test":            "",
	}
	for in, want := range cases {
		if got := stripOwnHost(in, own); got != want {
			t.Errorf("stripOwnHost(%q)=%q want %q", in, got, want)
		}
	}
}
