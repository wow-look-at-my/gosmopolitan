# Gosmopolitan src build
# Sets cosmo environment for build recipes

export PATH := $(CURDIR)/../bin:$(PATH)
export GOOS := cosmo
export GOARCH := amd64

GO := $(CURDIR)/../bin/go

# Find all Go source files
GO_SOURCES := $(shell find . -name '*.go' -o -name '*.s' -o -name '*.bash' 2>/dev/null)

# The go binary (built by make.bash)
$(GO): $(GO_SOURCES)
	./make.bash

.PHONY: build
build: $(GO)

# Build fizzbuzz test binary
$(FIZZBUZZ_BIN_PRISTINE): $(GO) ../testdata/fizzbuzz/fizzbuzz.go
	rm -f $@
	$(GO) build -o $@ ../testdata/fizzbuzz/fizzbuzz.go
	chmod -x $@

$(FIZZBUZZ_BIN): $(FIZZBUZZ_BIN_PRISTINE)
	cp $< $@
	chmod +x $@

build-fizzbuzz: $(FIZZBUZZ_BIN)
