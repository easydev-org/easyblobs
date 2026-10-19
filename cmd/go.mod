module github.com/easydev-org/easyblobs/cmd

go 1.26.5

require (
	github.com/easydev-org/easyblobs/apk v0.0.0
	github.com/easydev-org/easyblobs/cargo v0.0.0
	github.com/easydev-org/easyblobs/composer v0.0.0
	github.com/easydev-org/easyblobs/conan v0.0.0
	github.com/easydev-org/easyblobs/conda v0.0.0
	github.com/easydev-org/easyblobs/core v0.14.0
	github.com/easydev-org/easyblobs/debian v0.0.0
	github.com/easydev-org/easyblobs/generic v0.0.0
	github.com/easydev-org/easyblobs/gitlfs v0.0.0
	github.com/easydev-org/easyblobs/go v0.0.0
	github.com/easydev-org/easyblobs/helm v0.0.0
	github.com/easydev-org/easyblobs/hex v0.0.0
	github.com/easydev-org/easyblobs/httpcache v0.0.0
	github.com/easydev-org/easyblobs/huggingface v0.0.0
	github.com/easydev-org/easyblobs/ivy v0.0.0
	github.com/easydev-org/easyblobs/maven v0.0.0
	github.com/easydev-org/easyblobs/nix v0.0.0
	github.com/easydev-org/easyblobs/npm v0.0.0
	github.com/easydev-org/easyblobs/nuget v0.0.0
	github.com/easydev-org/easyblobs/oci v0.0.0
	github.com/easydev-org/easyblobs/protobuf v0.0.0
	github.com/easydev-org/easyblobs/pub v0.0.0
	github.com/easydev-org/easyblobs/pypi v0.0.0
	github.com/easydev-org/easyblobs/rpm v0.0.0
	github.com/easydev-org/easyblobs/rubygems v0.0.0
	github.com/easydev-org/easyblobs/swiftpm v0.0.0
	github.com/easydev-org/easyblobs/system v0.0.0
	github.com/easydev-org/easyblobs/targets v0.1.2
)

require (
	github.com/aws/aws-sdk-go-v2 v1.47.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.5 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.113.1 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/easydev-org/easyblobs/netcache v0.1.0
	github.com/easydev-org/easyblobs/s3blob v0.0.0
	github.com/glebarez/go-sqlite v1.21.2 // indirect
	github.com/glebarez/sqlite v1.11.0 // indirect
	github.com/go-sql-driver/mysql v1.8.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/ulikunitz/xz v0.5.16 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gorm.io/driver/mysql v1.6.0 // indirect
	gorm.io/driver/postgres v1.6.2 // indirect
	gorm.io/gorm v1.31.2 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.58.0 // indirect
)

replace (
	github.com/easydev-org/easyblobs/apk => ../apk
	github.com/easydev-org/easyblobs/cargo => ../cargo
	github.com/easydev-org/easyblobs/composer => ../composer
	github.com/easydev-org/easyblobs/conan => ../conan
	github.com/easydev-org/easyblobs/conda => ../conda
	github.com/easydev-org/easyblobs/core => ../core
	github.com/easydev-org/easyblobs/debian => ../debian
	github.com/easydev-org/easyblobs/generic => ../generic
	github.com/easydev-org/easyblobs/gitlfs => ../gitlfs
	github.com/easydev-org/easyblobs/go => ../go
	github.com/easydev-org/easyblobs/helm => ../helm
	github.com/easydev-org/easyblobs/hex => ../hex
	github.com/easydev-org/easyblobs/httpcache => ../httpcache
	github.com/easydev-org/easyblobs/huggingface => ../huggingface
	github.com/easydev-org/easyblobs/ivy => ../ivy
	github.com/easydev-org/easyblobs/maven => ../maven
	github.com/easydev-org/easyblobs/nix => ../nix
	github.com/easydev-org/easyblobs/npm => ../npm
	github.com/easydev-org/easyblobs/nuget => ../nuget
	github.com/easydev-org/easyblobs/oci => ../oci
	github.com/easydev-org/easyblobs/protobuf => ../protobuf
	github.com/easydev-org/easyblobs/pub => ../pub
	github.com/easydev-org/easyblobs/pypi => ../pypi
	github.com/easydev-org/easyblobs/rpm => ../rpm
	github.com/easydev-org/easyblobs/rubygems => ../rubygems
	github.com/easydev-org/easyblobs/swiftpm => ../swiftpm
	github.com/easydev-org/easyblobs/system => ../system
	github.com/easydev-org/easyblobs/targets => ../targets
)

replace github.com/easydev-org/easyblobs/netcache => ../netcache

replace github.com/easydev-org/easyblobs/s3blob => ../s3blob
