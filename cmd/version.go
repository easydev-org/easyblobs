package main

import "os"

// appVersion is the deployed image tag, injected as APP_VERSION by the chart
// (falls back to "dev" for a local run). Surfaced in /healthz + /readyz so an
// operator can confirm which tag a pod is actually running.
func appVersion() string {
	if v := os.Getenv("APP_VERSION"); v != "" {
		return v
	}
	return "dev"
}
