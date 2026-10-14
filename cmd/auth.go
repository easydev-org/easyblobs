package main

import (
	"context"
	"log"
	"os"
	"strings"

	easyblobkit "github.com/easydev-org/easyblobs/core"
	"github.com/easydev-org/easyblobs/core/store"
)

// selectAuth builds the request-auth backend from the environment:
//
//	EASYBLOBS_AUTH=easyvcs|static   (default: static)
//	EASYBLOBS_EASYVCS_URL           easyvcs base URL (required for easyvcs)
//
// static (the default) is the historical behavior: --tokens seeds the local
// credential DB and an instance with no users is open (dev mode). easyvcs
// delegates every token to the easyvcs identity authority (core.RemoteAuth), so
// agent-gateway can pass a user's own token straight through; --tokens is not
// used in that mode. A nil return means the instance is open (no auth).
func selectAuth(meta *store.Store, tokens string) easyblobkit.Auth {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EASYBLOBS_AUTH"))) {
	case "easyvcs":
		url := strings.TrimSpace(os.Getenv("EASYBLOBS_EASYVCS_URL"))
		if url == "" {
			log.Fatalf("EASYBLOBS_AUTH=easyvcs requires EASYBLOBS_EASYVCS_URL")
		}
		log.Printf("auth: delegating identity to easyvcs at %s", url)
		return easyblobkit.NewRemoteAuth(url)
	case "", "static":
		if tokens != "" {
			if err := seedTokens(meta, tokens); err != nil {
				log.Fatalf("seed tokens: %v", err)
			}
		}
		if meta.OpenInstance(context.Background()) {
			log.Printf("auth: no users registered — open instance (anonymous read/write)")
			return nil
		}
		return easyblobkit.NewStoreAuth(meta)
	default:
		log.Fatalf("EASYBLOBS_AUTH=%q unknown (static|easyvcs)", os.Getenv("EASYBLOBS_AUTH"))
		return nil
	}
}
