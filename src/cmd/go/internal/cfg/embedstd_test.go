// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cfg

import (
	"errors"
	"strings"
	"testing"

	"internal/cosmo/embedded"
)

// A standard package whose Go files are all tests compiles to no archive, so
// embedstd writes its manifest entry with no archive name and adds nothing to
// the blob. crypto/internal/fips140test is one. Asking that entry for an
// archive reads the empty name out of the blob and kills the build, so it
// answers as a package this binary does not carry.
func TestEmbeddedStdArchivedSkipsAPackageWithNoArchive(t *testing.T) {
	t.Serial()
	manifestOnce.Do(func() {})
	manifestPkgs = map[string]*embedded.Package{
		"crypto/internal/fips140test": {ImportPath: "crypto/internal/fips140test"},
		"crypto/sha3":                 {ImportPath: "crypto/sha3", Archive: "std/cosmo_amd64/crypto/sha3.a"},
	}
	t.Cleanup(func() { manifestPkgs = nil })

	if pkg := EmbeddedStdArchived("crypto/internal/fips140test"); pkg != nil {
		t.Errorf("a test-only package answers %+v, want nil", pkg)
	}
	if pkg := EmbeddedStdArchived("crypto/sha3"); pkg == nil {
		t.Error("a package the blob carries answers nil")
	}
	if pkg := EmbeddedStdArchived("example.com/nothing"); pkg != nil {
		t.Errorf("a package the manifest never names answers %+v, want nil", pkg)
	}
	// The manifest still names it: the package is standard, and only its
	// archive is absent.
	if pkg := EmbeddedStdPackage("crypto/internal/fips140test"); pkg == nil {
		t.Error("the manifest no longer names a test-only standard package")
	}
}

// A go command that embeds its standard library carries one target set and no
// other. The message names the GOOS and GOARCH that asked for another, so the
// reader looks at those rather than at a binary they take to be broken.
func TestTargetMessageNamesTheTargetAsked(t *testing.T) {
	carried := []string{"cosmo/amd64", "cosmo/arm64"}
	msg := targetMessage(carried, "linux", "amd64", errors.New(`no embedded entry "manifest/linux_amd64"`))

	for _, want := range []string{
		"builds for cosmo/amd64 and cosmo/arm64",
		"GOOS=linux GOARCH=amd64 names linux/amd64",
		"Leave GOOS and GOARCH unset",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not say %q:\n%s", want, msg)
		}
	}
	// The blob's own wording says nothing a caller can act on.
	if strings.Contains(msg, "no embedded entry") {
		t.Errorf("message repeats the blob's wording instead of the cause:\n%s", msg)
	}
}

// A binary carrying nothing is a different fault, and saying a target is wrong
// would point at the wrong thing.
func TestTargetMessageWithNothingCarried(t *testing.T) {
	msg := targetMessage(nil, "cosmo", "amd64", errors.New("blob is empty"))

	if !strings.Contains(msg, "carries no standard library at all") {
		t.Errorf("message does not report an empty binary:\n%s", msg)
	}
	if strings.Contains(msg, "Leave GOOS and GOARCH unset") {
		t.Errorf("message blames GOOS and GOARCH for an empty binary:\n%s", msg)
	}
}
