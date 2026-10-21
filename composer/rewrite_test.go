package composer

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRewriteDistPointsUpstreamAtTheMirror pins the fix for a real bypass:
// Packagist's dist.url is an api.github.com zipball, so a client resolving
// through this mirror still downloaded the archive from GitHub - the mirror
// served metadata only. rewriteDist must repoint every version at /dist/.
func TestRewriteDistPointsUpstreamAtTheMirror(t *testing.T) {
	s := &State{SelfBase: "http://mirror.test"}

	upstream := `{"minified":"composer/2.0","packages":{"psr/container":[
	  {"name":"psr/container","version":"2.0.2",
	   "dist":{"type":"zip","url":"https://api.github.com/repos/php-fig/container/zipball/abc"}},
	  {"name":"psr/container","version":"1.1.2",
	   "dist":{"type":"zip","url":"https://api.github.com/repos/php-fig/container/zipball/def"}}
	]}}`

	out := s.rewriteDist("psr/container", []byte(upstream))

	var doc struct {
		Packages map[string][]struct {
			Version string `json:"version"`
			Dist    struct {
				URL string `json:"url"`
			} `json:"dist"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	got := doc.Packages["psr/container"]
	if len(got) != 2 {
		t.Fatalf("lost versions: got %d, want 2", len(got))
	}
	for _, v := range got {
		if strings.Contains(v.Dist.URL, "api.github.com") {
			t.Fatalf("version %s still points at GitHub: %s", v.Version, v.Dist.URL)
		}
		if !strings.Contains(v.Dist.URL, "/dist/psr/container/") {
			t.Fatalf("version %s dist.url = %q, want it under /dist/psr/container/", v.Version, v.Dist.URL)
		}
		if !strings.Contains(v.Dist.URL, v.Version) {
			t.Fatalf("version %s dist.url = %q, want the version in the path", v.Version, v.Dist.URL)
		}
	}
}

// TestRewriteDistPassesThroughUnknownJSON: metadata this adapter does not
// understand must be returned unchanged rather than corrupted or emptied.
func TestRewriteDistPassesThroughUnknownJSON(t *testing.T) {
	s := &State{SelfBase: "http://mirror.test"}
	for _, in := range []string{
		`not json at all`,
		`{"packages":{}}`,
		`{"packages":{"other/pkg":[{"version":"1.0.0"}]}}`,
	} {
		if got := string(s.rewriteDist("psr/container", []byte(in))); got != in {
			t.Fatalf("rewriteDist changed an unrecognised body:\n in: %s\nout: %s", in, got)
		}
	}
}
