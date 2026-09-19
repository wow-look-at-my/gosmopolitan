// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// writeTree writes each file of the tree, named in slash form relative to root,
// and answers root.
func writeTree(test *testing.T, root string, files map[string]string) string {
	test.Helper()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			test.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o666); err != nil {
			test.Fatal(err)
		}
	}
	return root
}

// The overlay carries what the generators added, so additions answers exactly
// the files the stage has and the module does not.
func TestAdditionsAnswersTheAddedFiles(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod": "module example.com/m\n",
		"api.go": "package m\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                 "module example.com/m\n",
		"api.go":                 "package m\n",
		"zz_generated.go":        "package m\n",
		"internal/deep/table.go": "package deep\n",
	})

	got, err := additions(modroot, stage)
	if err != nil {
		test.Fatalf("additions: %v", err)
	}
	want := []string{"internal/deep/table.go", "zz_generated.go"}
	if !slices.Equal(got, want) {
		test.Errorf("additions = %q, want %q", got, want)
	}
}

// What a module's authors published is the module. So a file both trees hold
// keeps the zip's bytes, and it is not an addition however the generator
// rewrote it.
func TestAdditionsKeepsTheModulesOwnBytes(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"api.go": "package m\n\nconst Published = 1\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"api.go": "package m\n\nconst Published = 2\n",
	})

	got, err := additions(modroot, stage)
	if err != nil {
		test.Fatalf("additions: %v", err)
	}
	if len(got) != 0 {
		test.Errorf("additions = %q, want none: the module already carries that file", got)
	}
}

// A directive is a line the module carries. An alias line declares a name for
// later lines and generates nothing itself, so it is not one.
func TestDirectivesCountsGenerateLines(test *testing.T) {
	dir := writeTree(test, test.TempDir(), map[string]string{
		"a.go": "package m\n" +
			"\n" +
			"//go:generate -command stringer go run golang.org/x/tools/cmd/stringer\n" +
			"//go:generate stringer -type=Kind\n" +
			"\t//go:generate\tgo run ./gen\n" +
			"//go:generateoops not a directive\n" +
			"//go:generate\n" +
			"// go:generate spaced out\n",
		"b.go": "package m\n\n//go:generate go run ./other\n",
	})
	files := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}

	if got := directives(files); got != 3 {
		test.Fatalf("directives = %d, want 3", got)
	}

	// The module's own bytes decide what it generates. A count that also asked
	// what this machine has installed would make one module version mean two
	// things across a fleet that shares one cache key.
	test.Setenv("PATH", test.TempDir())
	if got := directives(files); got != 3 {
		test.Errorf("directives = %d with an empty PATH, want the same 3", got)
	}
}

// A nested module is its own module with its own zip, so its packages are not
// this module's to generate. Every ordinary subdirectory is, and the order is
// the module's own so that every machine generates in the same one.
func TestGeneratingPackagesSkipsNestedModules(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":           "module example.com/m\n",
		"a.go":             "package m\n\n//go:generate go run ./gen\n",
		"sub/b.go":         "package sub\n\n//go:generate go run ./gen\n",
		"quiet/e.go":       "package quiet\n",
		"nested/go.mod":    "module example.com/m/nested\n",
		"nested/c.go":      "package nested\n\n//go:generate go run ./gen\n",
		"nested/deep/d.go": "package deep\n\n//go:generate go run ./gen\n",
		"sub/notes.txt":    "//go:generate go run ./gen\n",
	})

	got := Packages(modroot)
	want := []string{".", "sub"}
	if !slices.Equal(got, want) {
		test.Errorf("Packages = %v, want %v", got, want)
	}
}

// A run that failed contributes nothing, so what appeared during it has to be
// separable from what the runs before it left.
func TestAppearedNamesOnlyTheNewFiles(test *testing.T) {
	kept := []string{"one/one.gen.go", "two/two.gen.go"}
	grown := []string{"broken/broken.gen.go", "one/one.gen.go", "two/two.gen.go"}

	got := appeared(grown, kept)
	want := []string{"broken/broken.gen.go"}
	if !slices.Equal(got, want) {
		test.Errorf("appeared = %v, want %v", got, want)
	}
	if got := appeared(kept, kept); len(got) != 0 {
		test.Errorf("appeared with nothing new = %v, want none", got)
	}
}

// A missing program is this host's own gap, not the module's. Skipping the
// directive would hand this build a module that the same version elsewhere does
// not match, so the build stops instead.
func TestProgramMissingSeparatesTheHostFromTheModule(test *testing.T) {
	missing := &exec.Error{Name: "stringer", Err: exec.ErrNotFound}
	cases := []struct {
		why  string
		err  error
		want bool
	}{
		{"no error at all", nil, false},
		{"a directive names a program this machine lacks", missing, true},
		{"the build reports that failure around its own", fmt.Errorf("running go generate: %w", missing), true},
		{"a generator ran and failed", errors.New("exit status 1"), false},
	}
	for _, tcase := range cases {
		if got := programMissing(tcase.err); got != tcase.want {
			test.Errorf("programMissing where %s = %v, want %v", tcase.why, got, tcase.want)
		}
		if got := hostCannotGenerate(tcase.err); got != tcase.want {
			test.Errorf("hostCannotGenerate where %s = %v, want %v", tcase.why, got, tcase.want)
		}
	}
}

// ansiTree stages the shape that broke: the module ships a table it builds at
// run time, and its generator writes a precomputed one beside it under another
// name. Both declare Table, so adding the generated file stops the package
// compiling. This is github.com/charmbracelet/x/ansi.
func ansiTree(test *testing.T) (modroot, stage string) {
	test.Helper()
	const shipped = "package parser\n\ntype TransitionTable []byte\n\nvar Table = GenerateTransitionTable()\n"
	const generated = "package parser\n\n// Code generated by gen.go. DO NOT EDIT.\n\nvar Table = TransitionTable{1}\n"
	modroot = writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
	})
	stage = writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
		"parser/table.go":            generated,
	})
	return modroot, stage
}

func TestARedeclaringAdditionIsDropped(test *testing.T) {
	modroot, stage := ansiTree(test)

	kept, err := withoutRedeclarations(modroot, stage, "example.com/ansi", []string{"parser/table.go"})
	if err != nil {
		test.Fatal(err)
	}
	if len(kept) != 0 {
		test.Errorf("kept %v, want the module's own file to win", kept)
	}
}

// The case this exists for: a module that ships without its generated half.
func TestAnAdditionTheModuleLacksIsKept(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"pkg/use.go": "package pkg\n\nfunc Use() string { return Name }\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"pkg/use.go":       "package pkg\n\nfunc Use() string { return Name }\n",
		"pkg/generated.go": "package pkg\n\nconst Name = \"x\"\n",
	})

	kept, err := withoutRedeclarations(modroot, stage, "example.com/m", []string{"pkg/generated.go"})
	if err != nil {
		test.Fatal(err)
	}
	if !slices.Equal(kept, []string{"pkg/generated.go"}) {
		test.Errorf("kept %v, want the generated half kept", kept)
	}
}

// Two files the compiler never reads together may declare the same name, so a
// constrained file on either side is no collision.
func TestAConstrainedFileIsNotACollision(test *testing.T) {
	const shipped = "//go:build windows\n\npackage pkg\n\nvar Table = 1\n"
	cases := []struct {
		why   string
		mine  map[string]string
		added string
	}{
		{"a build line constrains the module's file", map[string]string{"pkg/shipped.go": shipped}, "pkg/table.go"},
		{"a name suffix constrains it", map[string]string{"pkg/table_windows.go": "package pkg\n\nvar Table = 1\n"}, "pkg/table.go"},
	}
	for _, tcase := range cases {
		modroot := writeTree(test, test.TempDir(), tcase.mine)
		staged := map[string]string{tcase.added: "package pkg\n\nvar Table = 2\n"}
		for rel, body := range tcase.mine {
			staged[rel] = body
		}
		stage := writeTree(test, test.TempDir(), staged)

		kept, err := withoutRedeclarations(modroot, stage, "example.com/m", []string{tcase.added})
		if err != nil {
			test.Fatal(err)
		}
		if len(kept) != 1 {
			test.Errorf("kept %v where %s, want the addition kept", kept, tcase.why)
		}
	}
}

// A method hangs off its receiver, so it is no package-level name at all.
func TestAMethodDoesNotCollide(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"pkg/a.go": "package pkg\n\ntype T struct{}\n\nfunc (T) Do() {}\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"pkg/a.go": "package pkg\n\ntype T struct{}\n\nfunc (T) Do() {}\n",
		"pkg/b.go": "package pkg\n\ntype U struct{}\n\nfunc (U) Do() {}\n",
	})

	kept, err := withoutRedeclarations(modroot, stage, "example.com/m", []string{"pkg/b.go"})
	if err != nil {
		test.Fatal(err)
	}
	if len(kept) != 1 {
		test.Errorf("kept %v, want a method on another receiver kept", kept)
	}
}

func TestConstrainedNameReadsThePlatformSuffix(test *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"table.go", false},
		{"linux.go", false},
		{"table_helper.go", false},
		{"table_linux.go", true},
		{"table_amd64.go", true},
		{"table_linux_amd64.go", true},
		{"table_linux_test.go", true},
	}
	for _, tcase := range cases {
		if got := constrainedName(tcase.name); got != tcase.want {
			test.Errorf("constrainedName(%q) = %v, want %v", tcase.name, got, tcase.want)
		}
	}
}
