// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package exportdata

import (
	"go/build"
	"internal/testenv"
	"os"
	"path/filepath"
	"testing"
)

// TestLookupRecordsDependencies: the lookup of one package records the export
// data of its dependencies, so a type check starts one go command, not one per
// import.
func TestLookupRecordsDependencies(t *testing.T) {
	testenv.MustHaveGoBuild(t)

	dir := filepath.Join(build.Default.GOROOT, "src", "strings")
	export, err := lookupGorootExport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(export); err != nil {
		t.Fatalf("the export data of strings: %v", err)
	}

	dep, err := build.Import("internal/bytealg", "", build.FindOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := exportMap.Load(dep.Dir); !ok {
		t.Fatalf("the lookup of strings did not record its dependency internal/bytealg at %s", dep.Dir)
	}
	depExport, err := lookupGorootExport(dep.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(depExport); err != nil {
		t.Fatalf("the export data of internal/bytealg: %v", err)
	}
	if depExport == export {
		t.Fatalf("strings and internal/bytealg share the export file %s", export)
	}
}
