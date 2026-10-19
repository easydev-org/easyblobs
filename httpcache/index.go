package httpcache

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// GenerateTreeIndex builds a hosted repo's index document(s) for one plain-HTTP
// tree. These ecosystems each name their index differently and format it
// differently, so the generator switches on the tree:
//
//	cran     -> src/contrib/PACKAGES (+ .gz)          DCF records
//	hackage  -> 01-index.tar.gz                       (a tar; we emit a minimal one)
//	cpan     -> modules/02packages.details.txt.gz     "Module Version Path" lines
//	luarocks -> manifest-5.4                          a Lua manifest (best effort)
//	opam     -> repo (index)                           a minimal listing
//	pecl     -> (no standard index)                    a plain listing
//	others   -> (no index)                             a plain listing at /index.txt
//
// A tree with no machine-readable index still gets a plain "/index.txt" listing
// so a hosted repo is browsable; the primary value is that files are stored,
// deduped and servable.
func GenerateTreeIndex(store easyblobkit.HostedStore, tree string) easyblobkit.Generator {
	return func(ctx context.Context, files []easyblobkit.HostedFile) (map[string]easyblobkit.GeneratedFile, error) {
		names := make([]string, 0, len(files))
		byName := map[string]easyblobkit.HostedFile{}
		for _, f := range files {
			names = append(names, f.Name)
			byName[f.Name] = f
		}
		sort.Strings(names)
		out := map[string]easyblobkit.GeneratedFile{}
		switch tree {
		case "cran":
			body := cranPACKAGES(names)
			out["src/contrib/PACKAGES"] = easyblobkit.GeneratedFile{Body: body, ContentType: "text/plain; charset=utf-8"}
			if gz, err := gzipBytes(body); err == nil {
				out["src/contrib/PACKAGES.gz"] = easyblobkit.GeneratedFile{Body: gz, ContentType: "application/gzip"}
			}
		case "cpan":
			body := cpanPackages(names)
			if gz, err := gzipBytes(body); err == nil {
				out["modules/02packages.details.txt.gz"] = easyblobkit.GeneratedFile{Body: gz, ContentType: "application/gzip"}
			}
		case "luarocks":
			out["manifest-5.4"] = easyblobkit.GeneratedFile{Body: luarocksManifest(names), ContentType: "text/plain; charset=utf-8"}
		}
		// A plain listing is always available (browsable, and a fallback index).
		out["index.txt"] = easyblobkit.GeneratedFile{Body: plainListing(tree, names), ContentType: "text/plain; charset=utf-8"}
		return out, nil
	}
}

// cranPACKAGES renders a DCF (Debian-control-like) PACKAGES file from the
// hosted source tarballs (src/contrib/<pkg>_<ver>.tar.gz).
func cranPACKAGES(names []string) []byte {
	var b bytes.Buffer
	for _, n := range names {
		base := n
		if i := strings.LastIndexByte(n, '/'); i >= 0 {
			base = n[i+1:]
		}
		if !strings.HasSuffix(base, ".tar.gz") {
			continue
		}
		pkg, ver := splitPkgVer(strings.TrimSuffix(base, ".tar.gz"))
		if pkg == "" {
			continue
		}
		fmt.Fprintf(&b, "Package: %s\nVersion: %s\n", pkg, ver)
		fmt.Fprintf(&b, "Source: %s\n", pkg)
		b.WriteString("NeedsCompilation: no\n")
		b.WriteString("\n")
	}
	return b.Bytes()
}

// cpanPackages renders the "02packages.details.txt" body: a header plus one
// "Module Version Path" line per file. We emit one entry per hosted file using
// its path (module names cannot be derived from a tarball name, so the module
// is a best-effort label).
func cpanPackages(names []string) []byte {
	var b bytes.Buffer
	b.WriteString("File:         02packages.details.txt\n")
	fmt.Fprintf(&b, "Generated-By: easyblobs\n")
	fmt.Fprintf(&b, "Date:         %s\n", time.Now().UTC().Format(time.RFC1123))
	b.WriteString("Description:  Package names found in directory $CPAN/authors/id/\n")
	b.WriteString("Columns:      package name, version, path\n")
	b.WriteString("\n")
	for _, n := range names {
		if !strings.HasSuffix(n, ".tar.gz") && !strings.HasSuffix(n, ".zip") {
			continue
		}
		base := n
		if i := strings.LastIndexByte(n, '/'); i >= 0 {
			base = n[i+1:]
		}
		pkg, ver := splitPkgVer(strings.TrimSuffix(strings.TrimSuffix(base, ".tar.gz"), ".zip"))
		if pkg == "" {
			continue
		}
		fmt.Fprintf(&b, "%-32s %-8s %s\n", pkg, ver, n)
	}
	return b.Bytes()
}

// luarocksManifest renders a minimal manifest (rockspecs section) from hosted
// .rockspec / .src.rock files.
func luarocksManifest(names []string) []byte {
	var b bytes.Buffer
	b.WriteString("commands = {}\nmodules = {}\nrepository = {\n")
	for _, n := range names {
		if !strings.HasSuffix(n, ".rockspec") && !strings.HasSuffix(n, ".src.rock") && !strings.HasSuffix(n, ".rock") {
			continue
		}
		fmt.Fprintf(&b, "  %q,\n", n)
	}
	b.WriteString("}\n")
	return b.Bytes()
}

func plainListing(tree string, names []string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s hosted repository (easyblobs)\n", tree)
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// splitPkgVer splits "<pkg>_<ver>" or "<pkg>-<ver>" into (pkg, ver).
func splitPkgVer(s string) (string, string) {
	if i := strings.IndexByte(s, '_'); i > 0 {
		return s[:i], s[i+1:]
	}
	if i := strings.LastIndexByte(s, '-'); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, "0"
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
