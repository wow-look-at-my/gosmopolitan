// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package build

import (
	"internal/testenv"
	"runtime"
	"strings"
	"testing"
)

// Prefixes for packages that can be vendored into the go repo.
// The prefixes are component-wise; for example, "golang.org/x"
// matches "golang.org/x/build" but not "golang.org/xyz".
//
// DO NOT ADD TO THIS LIST TO FIX BUILDS.
// Vendoring a new package requires prior discussion.
var allowedPackagePrefixes = []string{
	"golang.org/x",
	"github.com/google/pprof",
	"github.com/ianlancetaylor/demangle",
	"rsc.io/markdown",

	// Fork-local: cmd/link compresses cosmo .debug_* sections with
	// ELFCOMPRESS_ZSTD (see cmd/link/internal/ld/dwarfcompress_zstd.go).
	"github.com/klauspost/compress",

	// Fork-local: cmd/go talks to the org's shared build cache in process
	// (see cmd/go/internal/cache/shared.go). lz4 is the cache's wire framing
	// and go-containers/set is the client's own dependency.
	// The client's broker reaches its children over go-ipc, which builds its
	// queue on go-shm, which maps the segment with go-mmap.
	"github.com/wow-look-at-my/go-s3-server/cacheclient",
	"github.com/wow-look-at-my/go-containers",
	"github.com/wow-look-at-my/go-ipc",
	"github.com/wow-look-at-my/go-shm",
	"github.com/wow-look-at-my/go-mmap",
	"github.com/pierrec/lz4",

	// Fork-local: the cache's broker serves one build's directory to every
	// process below it over shared memory. go-shm and go-mmap are what
	// go-ipc maps that memory with.
	"github.com/wow-look-at-my/go-ipc",
	"github.com/wow-look-at-my/go-shm",
	"github.com/wow-look-at-my/go-mmap",

	// Fork-local: every cmd test writes its assertions with testify. The
	// three below are what testify itself requires.
	"github.com/stretchr/testify",
	"github.com/davecgh/go-spew",
	"github.com/pmezard/go-difflib",
	"go.yaml.in/yaml",
}

// Verify that the vendor directories contain only packages matching the list above.
func TestVendorPackages(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	goBin := testenv.GoToolPath(t)
	listCmd := testenv.Command(t, goBin, "list", "std", "cmd")
	out, err := listCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, fullPkg := range strings.Split(string(out), "\n") {
		pkg, found := strings.CutPrefix(fullPkg, "vendor/")
		if !found {
			_, pkg, found = strings.Cut(fullPkg, "/vendor/")
			if !found {
				continue
			}
		}
		if !isAllowed(pkg) {
			t.Errorf(`
		Package %q should not be vendored into this repo.
		After getting approval from the Go team, add it to allowedPackagePrefixes
		in %s.`,
				pkg, thisFile)
		}
	}
}

func isAllowed(pkg string) bool {
	for _, pre := range allowedPackagePrefixes {
		if pkg == pre || strings.HasPrefix(pkg, pre+"/") {
			return true
		}
	}
	return false
}

func TestIsAllowed(t *testing.T) {
	for _, test := range []struct {
		in   string
		want bool
	}{
		{"evil.com/bad", false},
		{"golang.org/x/build", true},
		{"rsc.io/markdown", true},
		{"rsc.io/markdowntonabbey", false},
		{"rsc.io/markdown/sub", true},
	} {
		got := isAllowed(test.in)
		if got != test.want {
			t.Errorf("%q: got %t, want %t", test.in, got, test.want)
		}
	}
}
