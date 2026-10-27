package arch

// The repository database, generated from hosted packages.
//
// pacman reads <repo>.db as a GZIPPED TAR whose members are one directory per
// package, each containing a `desc` file. The desc file is sections introduced
// by %UPPERCASE% lines and terminated by a blank line:
//
//	%FILENAME%
//	cracklib-2.10.3-1-x86_64.pkg.tar.zst
//
//	%NAME%
//	cracklib
//
//	%VERSION%
//	2.10.3-1
//
//	%CSIZE%
//	283854
//
//	%SHA256SUM%
//	443746c7...
//
// That shape was read off a real core.db (geo.mirror.pkgbuild.com) rather than
// recalled, and the field set below is the subset pacman actually requires to
// install: without %FILENAME% it cannot find the package, and without
// %SHA256SUM% it cannot verify what it downloaded.
//
// WHY GENERATED: the operator uploads .pkg.tar.zst files. The database that
// describes them (name, version, size, checksum) is derived from those bytes;
// hand-writing a desc alongside each package would let the two disagree, and
// pacman trusts the desc.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	easyblobkit "github.com/easydev-org/easyblobs/core"
)

// pkgEntry is one package's database record.
type pkgEntry struct {
	Filename string
	Name     string
	Version  string
	Desc     string
	CSize    int64
	ISize    int64
	SHA256   string
}

// dbEntryDir is the tar member name for one package: "<name>-<version>/desc".
//
// pacman keys entries by that directory name, so it must be the package's real
// filename stem - a mismatch makes pacman report the package under one name and
// fetch it under another.
func dbEntryDir(e pkgEntry) string {
	return e.Name + "-" + e.Version
}

// renderDesc produces the desc body for one entry.
func renderDesc(e pkgEntry) []byte {
	var b bytes.Buffer
	section := func(key, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%%%s%%\n%s\n\n", key, value)
	}
	section("FILENAME", e.Filename)
	section("NAME", e.Name)
	section("VERSION", e.Version)
	section("DESC", e.Desc)
	if e.CSize > 0 {
		section("CSIZE", fmt.Sprint(e.CSize))
	}
	if e.ISize > 0 {
		section("ISIZE", fmt.Sprint(e.ISize))
	}
	section("SHA256SUM", e.SHA256)
	return b.Bytes()
}

// GenerateDB returns the Generator for a hosted arch repository.
//
// Two names are served for the same bytes because clients disagree about which
// one to ask for: modern pacman fetches <repo>.db, older setups and several
// tools use <repo>.db.tar.gz. Serving only one means half the clients 404.
func GenerateDB(store easyblobkit.HostedStore) easyblobkit.Generator {
	return func(ctx context.Context, files []easyblobkit.HostedFile) (map[string]easyblobkit.GeneratedFile, error) {
		entries, err := collectPackages(ctx, store, files)
		if err != nil {
			return nil, err
		}

		body := buildDBTar(entries)
		gz, err := gzipBytes(body)
		if err != nil {
			return nil, err
		}

		// The repository name is the first path segment of every hosted file
		// (core/os/x86_64/x.pkg.tar.zst), so the database is named after it.
		repoName := "e2e"
		if len(files) > 0 {
			if name, _, ok := easyblobkit.SplitRepo(strings.Trim(files[0].Name, "/")); ok && name != "" {
				repoName = name
			}
		}

		out := map[string]easyblobkit.GeneratedFile{}
		for _, suffix := range []string{"db", "db.tar.gz"} {
			// The database lives beside the packages, under os/<arch>/.
			for _, archName := range archNames(files) {
				out[repoName+"/os/"+archName+"/"+repoName+"."+suffix] = easyblobkit.GeneratedFile{
					Body:        gz,
					ContentType: "application/gzip",
				}
			}
		}
		return out, nil
	}
}

// archNames lists the architectures present in a repo, so a database is
// generated for each rather than one guessing at x86_64.
func archNames(files []easyblobkit.HostedFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		parts := strings.Split(strings.Trim(f.Name, "/"), "/")
		// <repo>/os/<arch>/<file>
		if len(parts) >= 4 && parts[1] == "os" && parts[2] != "" && !seen[parts[2]] {
			seen[parts[2]] = true
			out = append(out, parts[2])
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		out = []string{"x86_64"}
	}
	return out
}

// collectPackages turns hosted .pkg.tar.zst files into database entries.
func collectPackages(ctx context.Context, store easyblobkit.HostedStore,
	files []easyblobkit.HostedFile) ([]pkgEntry, error) {

	var entries []pkgEntry
	for _, f := range files {
		base := f.Name
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		if !strings.HasPrefix(base, ".") && !strings.Contains(base, ".pkg.tar") {
			continue
		}
		if strings.HasPrefix(base, ".") {
			continue
		}

		data, err := readBlob(ctx, store, f.Digest)
		if err != nil {
			return nil, fmt.Errorf("blob %s unavailable: %w", f.Digest, err)
		}

		// The FILENAME is parsed, not the .PKGINFO inside the package.
		//
		// Reading .PKGINFO would mean decompressing zstd, and this module does
		// not depend on a zstd library. Arch's own repo-add derives the entry
		// the same way: <name>-<pkgver>-<pkgrel>-<arch>.pkg.tar.zst is a fixed
		// shape, and the last three dash-separated fields are unambiguous -
		// <name> is whatever remains, so a name containing dashes still works.
		name, version, archName := splitPkgFilename(base)
		if name == "" {
			continue
		}
		e := pkgEntry{
			Filename: base,
			Name:     name,
			Version:  version,
			CSize:    int64(len(data)),
			SHA256:   sha256Hex(data),
		}
		_ = archName
		entries = append(entries, e)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Version < entries[j].Version
	})
	return entries, nil
}

// buildDBTar assembles the uncompressed database tar: one directory entry and
// one desc file per package.
func buildDBTar(entries []pkgEntry) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	for _, e := range entries {
		dir := dbEntryDir(e)
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeDir, Name: dir + "/",
			Mode: 0o755, ModTime: now,
		}); err != nil {
			continue
		}
		desc := renderDesc(e)
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: dir + "/desc",
			Mode: 0o644, Size: int64(len(desc)), ModTime: now,
		}); err != nil {
			continue
		}
		if _, err := tw.Write(desc); err != nil {
			continue
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

// splitPkgFilename parses <name>-<pkgver>-<pkgrel>-<arch>.pkg.tar.<ext>.
//
// The LAST THREE dash-separated fields are <pkgver>, <pkgrel> and <arch>, so
// <name> is everything before them - which keeps a name that itself contains
// dashes intact (python-setuptools-scm-8.0.4-1-any). Parsing from the right is
// what makes that work; splitting from the left would not.
//
// This is how repo-add identifies a package without decompressing it, and it is
// used here for the same reason: no zstd dependency.
func splitPkgFilename(base string) (name, version, archName string) {
	stem := base
	matched := false
	for _, suf := range []string{".pkg.tar.zst", ".pkg.tar.xz", ".pkg.tar.gz",
		".pkg.tar.bz2", ".pkg.tar.lz4", ".pkg.tar.lzo", ".pkg.tar.Z"} {
		if strings.HasSuffix(stem, suf) {
			stem = strings.TrimSuffix(stem, suf)
			matched = true
			break
		}
	}
	if !matched {
		return "", "", ""
	}
	parts := strings.Split(stem, "-")
	if len(parts) < 4 {
		// Not enough fields to be <name>-<ver>-<rel>-<arch>; refuse rather than
		// publish a database entry pacman cannot use.
		return "", "", ""
	}
	n := len(parts)
	name = strings.Join(parts[:n-3], "-")
	if name == "" {
		return "", "", ""
	}
	version = parts[n-3] + "-" + parts[n-2]
	archName = parts[n-1]
	return name, version, archName
}

// gzipBytes compresses a body, used for the database and its .tar.gz alias.
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

// sha256Hex is the %SHA256SUM% form pacman verifies.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// readBlob reads one stored blob, matching the debian adapter's helper.
func readBlob(ctx context.Context, store easyblobkit.HostedStore, digest string) ([]byte, error) {
	rd, err := store.Registry.Blobs.Open(ctx, digest)
	if err != nil || rd == nil {
		return nil, fmt.Errorf("blob %s unavailable", digest)
	}
	defer func() { _ = rd.Close() }()
	return io.ReadAll(rd)
}
