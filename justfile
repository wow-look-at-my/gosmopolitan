set shell := ["bash", "-euo", "pipefail", "-c"]

mod env

# Required tools
export READELF := require("readelf")
export OTOOL := require("otool")
HOST_GO := require("go")

# Test binary paths
export FIZZBUZZ_BIN := justfile_directory() / "fizzbuzz-test.com"

# Build the Go toolchain
build: env::build

# Build APE binary and run tests (rebuilds toolchain if not on CI)
test: env::_maybe-build env::build-fizzbuzz
    cd testdata/ape/apetest && GOOS= GOARCH= GOROOT= {{HOST_GO}} test -v ./...
