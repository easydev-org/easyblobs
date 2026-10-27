// Package adapters blank-imports third-party protocol adapter packages so
// their Register() calls (via init) are pulled into the binary. To enable a
// protocol, add an import here and rebuild.
package adapters

import (
	_ "github.com/easydev-org/easyblobs/apk"
	_ "github.com/easydev-org/easyblobs/cargo"
	_ "github.com/easydev-org/easyblobs/composer"
	_ "github.com/easydev-org/easyblobs/conan"
	_ "github.com/easydev-org/easyblobs/conda"
	_ "github.com/easydev-org/easyblobs/debian"
	_ "github.com/easydev-org/easyblobs/arch"
	_ "github.com/easydev-org/easyblobs/generic"
	_ "github.com/easydev-org/easyblobs/gitlfs"
	_ "github.com/easydev-org/easyblobs/go"
	_ "github.com/easydev-org/easyblobs/helm"
	_ "github.com/easydev-org/easyblobs/hex"
	_ "github.com/easydev-org/easyblobs/httpcache"
	_ "github.com/easydev-org/easyblobs/huggingface"
	_ "github.com/easydev-org/easyblobs/ivy"
	_ "github.com/easydev-org/easyblobs/maven"
	_ "github.com/easydev-org/easyblobs/netcache"
	_ "github.com/easydev-org/easyblobs/nix"
	_ "github.com/easydev-org/easyblobs/npm"
	_ "github.com/easydev-org/easyblobs/nuget"
	_ "github.com/easydev-org/easyblobs/oci"
	_ "github.com/easydev-org/easyblobs/protobuf"
	_ "github.com/easydev-org/easyblobs/pub"
	_ "github.com/easydev-org/easyblobs/pypi"
	_ "github.com/easydev-org/easyblobs/rpm"
	_ "github.com/easydev-org/easyblobs/rubygems"
	_ "github.com/easydev-org/easyblobs/s3blob"
	_ "github.com/easydev-org/easyblobs/swiftpm"
	_ "github.com/easydev-org/easyblobs/system"
)
