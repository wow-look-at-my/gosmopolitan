# Gosmopolitan build system

.EXTRA_PREREQS := $(MAKEFILE_LIST) src/Makefile

# Required tools
READELF := $(shell command -v readelf 2>/dev/null)
OTOOL := $(shell command -v otool 2>/dev/null)
HOST_GO := $(shell command -v go 2>/dev/null)

export READELF
export OTOOL

export FIZZBUZZ_BIN_PRISTINE := $(CURDIR)/fizzbuzz.com
export FIZZBUZZ_BIN := $(CURDIR)/fizzbuzz-test.com

# Build the Go toolchain for cosmo
build:
	$(MAKE) -C src build

# Test source files
TEST_SOURCES := $(shell cd testdata/ape/apetest && $(HOST_GO) list -f '{{range .TestGoFiles}}{{$$.Dir}}/{{.}} {{end}}' ./...)

# Run tests
test: $(FIZZBUZZ_BIN) $(TEST_SOURCES)
	cd testdata/ape/apetest && GOOS= GOARCH= GOROOT= $(HOST_GO) test -v ./...

$(FIZZBUZZ_BIN):
	$(MAKE) -C src build-fizzbuzz
