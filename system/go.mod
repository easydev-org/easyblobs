module github.com/easydev-org/easyblobs/system

go 1.26.5

require (
	github.com/easydev-org/easyblobs/core v0.0.0
	github.com/easydev-org/easyblobs/targets v0.1.2
)

replace github.com/easydev-org/easyblobs/core => ../core

replace github.com/easydev-org/easyblobs/targets => ../targets
