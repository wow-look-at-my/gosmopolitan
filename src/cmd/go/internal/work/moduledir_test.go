// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"path/filepath"
	"testing"

	"cmd/go/internal/load"
	"cmd/go/internal/modinfo"
)

// modulePackage answers a package of module m at importPath, in dir.
func modulePackage(module *modinfo.ModulePublic, importPath, dir string) *load.Package {
	pkg := &load.Package{}
	pkg.ImportPath = importPath
	pkg.Dir = dir
	pkg.Module = module
	pkg.Internal.OrigImportPath = importPath
	return pkg
}

func TestModuleSourceRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout")
	mainModule := &modinfo.ModulePublic{Path: "example.com/m", Dir: root}
	replaced := &modinfo.ModulePublic{Path: "example.com/other", Version: "v1.2.3", Dir: filepath.Join(root, "other")}

	tests := []struct {
		about    string
		pkg      *load.Package
		wantName string
		wantDir  string
		wantOK   bool
	}{
		{"the root package", modulePackage(mainModule, "example.com/m", root), "example.com/m", root, true},
		{"a package below the root", modulePackage(mainModule, "example.com/m/internal/alpha", filepath.Join(root, "internal", "alpha")), "example.com/m", root, true},
		{"a module a replace points at a directory", modulePackage(replaced, "example.com/other/part", filepath.Join(root, "other", "part")), "example.com/other@v1.2.3", filepath.Join(root, "other"), true},
		{"a directory the import path does not name", modulePackage(mainModule, "example.com/m/internal/alpha", filepath.Join(root, "elsewhere")), "", "", false},
		{"an import path outside the module", modulePackage(mainModule, "command-line-arguments", root), "", "", false},
		{"no module", modulePackage(nil, "example.com/m", root), "", "", false},
		{"a module with no directory", modulePackage(&modinfo.ModulePublic{Path: "example.com/m"}, "example.com/m", root), "", "", false},
	}
	for _, test := range tests {
		name, dir, ok := moduleSourceRoot(test.pkg)
		if name != test.wantName || dir != test.wantDir || ok != test.wantOK {
			t.Errorf("%s: moduleSourceRoot = %q, %q, %v, want %q, %q, %v", test.about, name, dir, ok, test.wantName, test.wantDir, test.wantOK)
		}
	}
}

func TestCompilesFromModuleDirKeepsCgo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout")
	pkg := modulePackage(&modinfo.ModulePublic{Path: "example.com/m", Dir: root}, "example.com/m", root)
	if !compilesFromModuleDir(pkg) {
		t.Fatalf("a Go package of the main module does not compile from its module directory")
	}
	pkg.CgoFiles = []string{"bridge.go"}
	if compilesFromModuleDir(pkg) {
		t.Errorf("a cgo package compiles from its module directory: the C compiler writes that path")
	}
}

func TestRestoreSourceDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout")
	dir := filepath.Join(root, "internal", "alpha")
	pkg := modulePackage(&modinfo.ModulePublic{Path: "example.com/m", Dir: root}, "example.com/m/internal/alpha", dir)
	onDisk := dir + string(filepath.Separator)

	tests := []struct {
		about string
		out   string
		want  string
	}{
		{"a position at the start of a line", "example.com/m/internal/alpha/alpha.go:8:27: warning\n", onDisk + "alpha.go:8:27: warning\n"},
		{"a position after a tab", "note:\n\texample.com/m/internal/alpha/alpha.go:3: here\n", "note:\n\t" + onDisk + "alpha.go:3: here\n"},
		{"two positions", "example.com/m/internal/alpha/a.go:1: x example.com/m/internal/alpha/b.go:2: y\n", onDisk + "a.go:1: x " + onDisk + "b.go:2: y\n"},
		{"an import path below the package", "could not import example.com/m/internal/alpha/sub (no file)\n", "could not import example.com/m/internal/alpha/sub (no file)\n"},
		{"a file of a package below", "example.com/m/internal/alpha/sub/sub.go:1: x\n", "example.com/m/internal/alpha/sub/sub.go:1: x\n"},
		{"a quoted import path", "\"example.com/m/internal/alpha/alpha.go:1\" in a string\n", "\"example.com/m/internal/alpha/alpha.go:1\" in a string\n"},
		{"no mention", "plain text\n", "plain text\n"},
	}
	for _, test := range tests {
		if got := restoreSourceDir(test.out, pkg); got != test.want {
			t.Errorf("%s: restoreSourceDir(%q) = %q, want %q", test.about, test.out, got, test.want)
		}
	}

	// The cache keeps the trimmed text, and a print restores it.
	live := onDisk + "alpha.go:8:27: warning\n"
	kept := trimSourceDir(live, pkg)
	if want := "example.com/m/internal/alpha/alpha.go:8:27: warning\n"; kept != want {
		t.Errorf("trimSourceDir(%q) = %q, want %q", live, kept, want)
	}
	if got := restoreSourceDir(kept, pkg); got != live {
		t.Errorf("restoreSourceDir(trimSourceDir(%q)) = %q", live, got)
	}

	// Text that names the directory and is no position stays as written, so
	// the restore has nothing it could get wrong.
	note := "wrote " + onDisk + "\n"
	if got := trimSourceDir(note, pkg); got != note {
		t.Errorf("trimSourceDir(%q) = %q, want it unchanged", note, got)
	}
}

func TestNamesLocalDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "checkout")
	pkg := modulePackage(&modinfo.ModulePublic{Path: "example.com/m", Dir: root}, "example.com/m/bad", filepath.Join(root, "bad"))
	if namesLocalDir([]byte("{}\n"), pkg) {
		t.Errorf("an empty JSON tree names a local directory")
	}
	if !namesLocalDir([]byte(`{"posn": "`+filepath.ToSlash(filepath.Join(root, "bad", "bad.go"))+`:7:2"}`), pkg) {
		t.Errorf("a position in the checkout names no local directory")
	}
}
