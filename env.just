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

# The first uprev guardrail (CLAUDE.md): std must still compile for
# every cosmo arch and for the host. The execution suite imports a
# fraction of std, so a platform file that stops applying to cosmo is
# invisible without this. CI runs it on the ubuntu build leg; run it
# here before proposing a change to the runtime or to a std package.
build-std:
    GOARCH=amd64 go build std
    GOARCH=arm64 go build std
    GOOS=linux GOARCH=amd64 go build std

# The second uprev guardrail (CLAUDE.md), plus the two suites a change
# to the cosmo runtime or to a std package it feeds has to re-verify:
# net's host-nameserver path on the host, and the NT ABI pins under
# GOOS=cosmo (which needs the misc/cosmo exec wrappers on PATH).
test-std:
    GOOS=linux GOARCH=amd64 go test -short go/build cmd/internal/moddeps
    GOOS=linux GOARCH=amd64 go test -run 'TestNameserversFromHost|TestUnreadableConfServers|TestDNSReadConfig' net
    PATH="{{justfile_directory()}}/misc/cosmo:$PATH" go test -run 'TestNTFixedInfoLayout|TestNTCertContextLayout|TestCosmo' runtime
    GOOS=linux GOARCH=amd64 go test -run 'TestSystemCertPool|TestSystemRoots' crypto/x509

# Can the toolchain's own commands compile for cosmo? That is the
# question behind shipping the toolchain itself as fat APEs -- one
# artifact covering every host -- instead of a tarball per platform.
# Fat, because the shipped shape is the answer being checked.
build-tools-cosmo:
    go build -o /tmp/cosmotools/compile.com cmd/compile
    go build -o /tmp/cosmotools/link.com cmd/link
    go build -o /tmp/cosmotools/asm.com cmd/asm
    go build -o /tmp/cosmotools/go.com cmd/go

# Build the runtime probe as an APE and run it on this host. CI runs the
# same binary on all three, so a red here is a red there.
probe:
    rm -f {{justfile_directory()}}/runtimeprobe.com
    cd testdata/runtimeprobe && go build -o {{justfile_directory()}}/runtimeprobe.com .
    RUNTIMEPROBE_MARK=probe {{justfile_directory()}}/runtimeprobe.com probe --help -x --key=value trailing-arg

build-fizzbuzz:
    rm -f {{FIZZBUZZ_BIN_PRISTINE}}
    go build -o {{FIZZBUZZ_BIN_PRISTINE}} testdata/fizzbuzz/fizzbuzz.go
    chmod -x {{FIZZBUZZ_BIN_PRISTINE}}
    cp {{FIZZBUZZ_BIN_PRISTINE}} {{FIZZBUZZ_BIN}}
    chmod +x {{FIZZBUZZ_BIN}}
