// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"testing"

	"cmd/go/internal/load"
	"internal/cosmo/embedded"
)

// TestEmbeddedStdActionTestOnlyPackage pins the listing of a standard
// package whose manifest entry carries no archive, which is what a package
// holding only test files records.
func TestEmbeddedStdActionTestOnlyPackage(t *testing.T) {
	pkg := &embedded.Package{ImportPath: "crypto/internal/fips140test", Name: "fips140test"}
	pack := &load.Package{}
	pack.ImportPath = pkg.ImportPath
	builder := &Builder{NeedExport: true}
	act := builder.finishEmbeddedStdAction(&Action{}, pack, pkg)
	if act.Target != "" {
		t.Errorf("Target = %q, want the empty string", act.Target)
	}
	if act.built != "" {
		t.Errorf("built = %q, want the empty string", act.built)
	}
	if pack.Export != "" {
		t.Errorf("Export = %q, want the empty string", pack.Export)
	}
	if act.Mode != "embedded std" {
		t.Errorf("Mode = %q, want %q", act.Mode, "embedded std")
	}
}
