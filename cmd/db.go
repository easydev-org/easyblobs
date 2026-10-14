package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/easydev-org/easyblobs/core/store"
)

// openMeta opens the metadata (IndexStore) store on the configured backend.
//
// Backend selection is by environment so a stateless deployment can put the
// metadata in Postgres/MySQL while blobs live in S3 (see README "Stateless
// deployment"). The names are shared with the easyvcs engine, so one
// deployment configures both:
//
//	driver: EASYBLOBS_DB_DRIVER | EASYVCS_DB_DRIVER  (sqlite|postgres|mysql; default sqlite)
//	dsn:    EASYBLOBS_DB_DSN    | EASYVCS_DB_DSN
//
// With no driver set it opens sqlite at <dataDir>/pkglab.db — exactly the
// historical behavior, so an unconfigured instance is unchanged.
func openMeta(dataDir string) (*store.Store, error) {
	kind := strings.TrimSpace(firstEnv("EASYBLOBS_DB_DRIVER", "EASYVCS_DB_DRIVER"))
	dsn := strings.TrimSpace(firstEnv("EASYBLOBS_DB_DSN", "EASYVCS_DB_DSN"))

	switch {
	case kind == "":
		// Default backend: sqlite at the data dir (zero change).
		kind = store.KindSQLite
		if dsn == "" {
			dsn = filepath.Join(dataDir, "pkglab.db")
		}
	case !store.KnownKind(kind):
		// An explicitly-named driver we do not support: report it here rather
		// than as a missing DSN (the name is the first problem).
		return nil, fmt.Errorf("unsupported db driver %q (sqlite|postgres|mysql)", kind)
	case dsn == "":
		return nil, fmt.Errorf("metadata: %s requires a DSN (EASYBLOBS_DB_DSN or EASYVCS_DB_DSN)", kind)
	}
	return store.OpenStore(store.DriverConfig{Kind: kind, DSN: dsn})
}

// firstEnv returns the first non-empty value among the named environment
// variables ("" when none is set).
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
