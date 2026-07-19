# Environment configuration for gosmopolitan builds

# Environment
export PATH := justfile_directory() / "bin:" + env("PATH")
export GOOS := "cosmo"
export GOARCH := "amd64"
export FIZZBUZZ_BIN_PRISTINE := justfile_directory() / "fizzbuzz.com"
export FIZZBUZZ_BIN := justfile_directory() / "fizzbuzz-test.com"

# Build the Go toolchain
build:
    cd src && ./make.bash

[private]
_maybe-build:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "${CI:-}" ]; then
        cd src && ./make.bash
    fi

build-fizzbuzz:
    rm -f {{FIZZBUZZ_BIN_PRISTINE}}
    go build -o {{FIZZBUZZ_BIN_PRISTINE}} testdata/fizzbuzz/fizzbuzz.go
    chmod -x {{FIZZBUZZ_BIN_PRISTINE}}
    cp {{FIZZBUZZ_BIN_PRISTINE}} {{FIZZBUZZ_BIN}}
    chmod +x {{FIZZBUZZ_BIN}}
