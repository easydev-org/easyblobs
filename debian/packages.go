package debian

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/easydev-org/easyblobs/core"
)

// GeneratePackages renders a flat-repo "Packages" index from hosted .deb
// files. apt's flat format needs, per package: Package, Version,
// Architecture, Filename, Size, and a checksum line. We extract the control
// fields by reading the .deb's control member (ar → control.tar.* →
// ./control). When extraction fails, a minimal stanza keyed by filename is
// emitted so the repo is still usable with [trusted=yes].
//
// This index is UNSIGNED: the source line must carry [trusted=yes] (dev
// deployments). Pull-through packages keep upstream signatures untouched.
func GeneratePackages(store easyblobkit.HostedStore) easyblobkit.Generator {
	return func(ctx context.Context, files []easyblobkit.HostedFile) (map[string]easyblobkit.GeneratedFile, error) {
		var b strings.Builder
		sorted := make([]easyblobkit.HostedFile, len(files))
		copy(sorted, files)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
		for _, f := range sorted {
			if !strings.HasSuffix(f.Name, ".deb") {
				continue
			}
			ctrl, err := debControlFields(ctx, store, f)
			if err != nil || ctrl["Package"] == "" {
				// Minimal stanza: filename-derived name/version.
				name, ver := nameVersionFromDeb(f.Name)
				ctrl = map[string]string{"Package": name, "Version": ver, "Architecture": "amd64"}
			}
			b.WriteString("Package: " + ctrl["Package"] + "\n")
			b.WriteString("Version: " + ctrl["Version"] + "\n")
			if a := ctrl["Architecture"]; a != "" {
				b.WriteString("Architecture: " + a + "\n")
			}
			if d := ctrl["Depends"]; d != "" {
				b.WriteString("Depends: " + d + "\n")
			}
			if d := ctrl["Description"]; d != "" {
				b.WriteString("Description: " + strings.ReplaceAll(d, "\n", "\n ") + "\n")
			}
			b.WriteString("Filename: ./" + f.Name + "\n")
			fmt.Fprintf(&b, "Size: %d\n", f.Size)
			b.WriteString("SHA256: " + digestHex(f.Digest) + "\n")
			b.WriteString("\n")
		}
		body := []byte(b.String())
		gz, err := gzipBytes(body)
		if err != nil {
			return nil, err
		}
		out := map[string]easyblobkit.GeneratedFile{
			"Packages":    {Body: body, ContentType: "text/plain"},
			"Packages.gz": {Body: gz, ContentType: "application/gzip"},
		}
		// Acquire-By-Hash: publish the index under
		// by-hash/SHA256/<hex> too, so an apt source that opts in (a Release with
		// "Acquire-By-Hash: yes") can fetch by hash. The hex is the sha256 of the
		// bytes, which is exactly the CAS digest we would serve anyway.
		addByHash(out, "Packages", body)
		addByHash(out, "Packages.gz", gz)
		return out, nil
	}
}

// addByHash adds a "by-hash/SHA256/<hex>" alias for one generated file, so the
// bytes are reachable by content hash (relative to the directory the index
// lives in — the HostedHandler serves repo-relative paths, and a client under a
// binary-<arch> dir resolves by-hash/ next to it).
func addByHash(out map[string]easyblobkit.GeneratedFile, name string, body []byte) {
	sum := sha256.Sum256(body)
	alias := "by-hash/SHA256/" + hex.EncodeToString(sum[:])
	out[alias] = easyblobkit.GeneratedFile{Body: body, ContentType: out[name].ContentType}
}

// gzipBytes gzips a document (apt prefers Packages.gz over Packages).
func gzipBytes(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// debControlFields extracts the control stanza of a hosted .deb.
func debControlFields(ctx context.Context, store easyblobkit.HostedStore, f easyblobkit.HostedFile) (map[string]string, error) {
	data, err := readBlob(ctx, store, f.Digest)
	if err != nil {
		return nil, err
	}
	ctrl, err := extractDebControl(data)
	if err != nil {
		return nil, err
	}
	return parseControlStanza(ctrl), nil
}
