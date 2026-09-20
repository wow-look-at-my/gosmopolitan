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

// A module zip leaves out a directory whose name opens with an underscore, so a
// directive naming one cannot run for anybody who fetched the module. Such a
// module ships what the directive writes. Every other directive is the module's
// own, and a failure in one stops the build.
func TestGeneratorNotShippedNamesOnlyTheDroppedPath(test *testing.T) {
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":           "module example.com/m\n",
		"assert/gen.go":    "package assert\n\n//go:generate sh -c \"go run ../_codegen/main.go -out x.go\"\n",
		"rules/gen.go":     "package rules\n\n//go:generate go run example.com/m/cmd/rulegen -out y.go\n",
		"cmd/rulegen/m.go": "package main\n",
		"local/gen.go":     "package local\n\n//go:generate go run ./_tool\n",
		"local/_tool/m.go": "package main\n",
	})

	if got := generatorNotShipped(stage, "assert"); got != "../_codegen/main.go" {
		test.Errorf("generatorNotShipped(assert) = %q, want the dropped path", got)
	}
	// The generator is an ordinary package of the module, so it rode the zip.
	if got := generatorNotShipped(stage, "rules"); got != "" {
		test.Errorf("generatorNotShipped(rules) = %q, want none", got)
	}
	// An underscore path the module does carry is runnable, whatever its name.
	if got := generatorNotShipped(stage, "local"); got != "" {
		test.Errorf("generatorNotShipped(local) = %q, want none", got)
	}
}

// A missing program is this host's own gap, not the module's, so the directive
// is skipped and the answer marked partial. A generator that ran and failed is
// the module's own defect and reads differently, because that one stops the
// build.
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

// A generator can write a name the module already declares under another file
// name, which is github.com/charmbracelet/x/ansi: it ships a table it builds at
// run time, and its generator writes a precomputed one beside it. Both declare
// Table.
//
// The file is added anyway. Deciding which of the two a consumer wanted is not
// this package's to make, and the compiler names both declarations and their
// positions when it reads them together.
//
// What this reaches is the list Complete copies from, and the copy itself. It
// does not reach Complete, which runs a sandboxed generator. So a filter
// placed between the two inside Complete is not something this can see.
func TestARedeclaringAdditionIsStillAdded(test *testing.T) {
	const shipped = "package parser\n\ntype TransitionTable []byte\n\nvar Table = GenerateTransitionTable()\n"
	const generated = "package parser\n\n// Code generated by gen.go. DO NOT EDIT.\n\nvar Table = TransitionTable{1}\n"
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
		"parser/table.go":            generated,
	})

	added, err := additions(modroot, stage)
	if err != nil {
		test.Fatal(err)
	}
	if !slices.Equal(added, []string{"parser/table.go"}) {
		test.Fatalf("additions = %v, want the generated file", added)
	}
	for _, rel := range added {
		from := filepath.Join(stage, filepath.FromSlash(rel))
		if err := copyFile(from, filepath.Join(modroot, filepath.FromSlash(rel))); err != nil {
			test.Fatal(err)
		}
	}
	body, err := os.ReadFile(filepath.Join(modroot, "parser", "table.go"))
	if err != nil {
		test.Fatalf("the completed module does not hold the generated file: %v", err)
	}
	if string(body) != generated {
		test.Errorf("parser/table.go = %q, want the generator's own bytes", body)
	}
}
