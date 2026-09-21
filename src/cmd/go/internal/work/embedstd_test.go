// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"testing"

	"internal/cosmo/embedded"
)

// crypto/internal/fips140test holds test files alone, so it compiles to no
// archive and embedstd records the entry with an empty archive name. Reading
// that name asks the blob for entry "", which no blob carries.
func TestAnArchivelessStdPackageServesNothing(test *testing.T) {
	cases := []struct {
		name string
		pkg  *embedded.Package
		want bool
	}{
		{"absent from the manifest", nil, false},
		{"test files alone", &embedded.Package{ImportPath: "crypto/internal/fips140test"}, false},
		{"compiled", &embedded.Package{ImportPath: "fmt", Archive: "std/cosmo_amd64/fmt.a"}, true},
	}
	for _, each := range cases {
		if got := servesArchive(each.pkg); got != each.want {
			test.Errorf("%s: servesArchive = %v, want %v", each.name, got, each.want)
		}
	}
}

// A package the manifest never names and one it names with no archive are
// different answers. Reading both as absent sends the archive-less one down
// the tree path, which stops the go command on a binary that carries no tree.
func TestEmbeddedStdLookupPartsAbsentFromArchiveless(test *testing.T) {
	cases := []struct {
		name string
		pkg  *embedded.Package
		want embeddedStdOutcome
	}{
		{"absent from the manifest", nil, embeddedStdFromTree},
		{"test files alone", &embedded.Package{ImportPath: "crypto/internal/fips140test"}, embeddedStdNoArchive},
		{"compiled", &embedded.Package{ImportPath: "fmt", Archive: "std/cosmo_amd64/fmt.a"}, embeddedStdArchive},
	}
	for _, each := range cases {
		if got := embeddedStdLookup(each.pkg); got != each.want {
			test.Errorf("%s: embeddedStdLookup = %v, want %v", each.name, got, each.want)
		}
	}
}
