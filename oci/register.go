package oci

import "github.com/easydev-org/easyblobs/core"

func init() {
	easyblobkit.Register("oci", NewHandler)
	// The OCI repository's upstream is decided per registry host
	// (ghcr.io/quay.io/...), so expose the host as the namespace. Requests
	// without a host prefix keep the default upstream.
	easyblobkit.RegisterNamespace("oci", easyblobkit.OCIHostNamespace)
}
