// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// A directive that writes into a submodule's directory reports an absent path,
// because the parent's zip carries no submodule. x/crypto's x509roots is the
// module this came from: it writes fallback/bundle.go, and fallback is a
// module of its own.
//
// The staged tree decides, not the message. A path whose parent is there names
// a defect of the module, and that stops the build.
func TestWroteNowhereSeparatesTheZipsShapeFromADefect(test *testing.T) {
	stage := writeTree(test, test.TempDir(), map[string]string{
		"x509roots/gen_fallback_bundle.go": "package main\n",
		"x509roots/nss/nss.go":             "package nss\n",
	})
	const x509roots = "exit status 1\n" +
		`2026/09/20 16:52:50 failed to write to "fallback/bundle.go": open fallback/bundle.go: no such file or directory`
	const nssRoots = "exit status 1\n" +
		`2026/09/20 16:52:50 failed to write to "nss/roots.go": open nss/roots.go: no such file or directory`
	const idna = "exit status 1\n" +
		"Copying exported files failed: open ../../net/idna/idna.go: no such file or directory"
	cases := []struct {
		why  string
		err  error
		want string
	}{
		{"no error at all", nil, ""},
		{"a directive writes into a submodule's directory", errors.New(x509roots), "fallback/bundle.go"},
		{"the build reports that failure around its own", fmt.Errorf("generating x509roots: %w", errors.New(x509roots)), "fallback/bundle.go"},
		{"the directory it writes to is right there", errors.New(nssRoots), ""},
		{"a path of its own leads out of the module", errors.New(idna), "../../net/idna/idna.go"},
		{"a generator ran and failed", errors.New("exit status 1"), ""},
		{"a directive names a program this machine lacks", &exec.Error{Name: "stringer", Err: exec.ErrNotFound}, ""},
	}
	for _, tcase := range cases {
		if got := wroteNowhere(stage, "x509roots", tcase.err); got != tcase.want {
			test.Errorf("wroteNowhere where %s = %q, want %q", tcase.why, got, tcase.want)
		}
	}
}

// An org module's generators are this fleet's own, so they run. A stranger's
// run only where the module asked for it in its own go.mod, and the marker is
// the whole comment.
func TestAllowedRunsTheOrgAndWhoeverAsked(test *testing.T) {
	cases := []struct {
		why   string
		mod   string
		gomod string
		want  bool
	}{
		{"an org module", OrgPrefix + "go-s3-server", "module " + OrgPrefix + "go-s3-server\n", true},
		{"a package of one", OrgPrefix + "go-containers/set", "module " + OrgPrefix + "go-containers/set\n", true},
		{"a stranger that says nothing", "example.com/m", "module example.com/m\n", false},
		{"a stranger that asked", "example.com/m", "module example.com/m\n\n" + OptIn + "\n", true},
		{"an indented ask", "example.com/m", "module example.com/m\n\t" + OptIn + "\t\n", true},
		{"a go.mod that only mentions it", "example.com/m", "// this module does not use " + OptIn + " yet\nmodule example.com/m\n", false},
		{"a name that opens with the org's", "github.com/wow-look-at-my-not/m", "module github.com/wow-look-at-my-not/m\n", false},
	}
	for _, tcase := range cases {
		modroot := writeTree(test, test.TempDir(), map[string]string{"go.mod": tcase.gomod})
		if got := Allowed(modroot, tcase.mod); got != tcase.want {
			test.Errorf("Allowed where %s = %v, want %v", tcase.why, got, tcase.want)
		}
	}

	// A module published before modules carries no go.mod, so it carries no
	// opt-in either.
	bare := writeTree(test, test.TempDir(), map[string]string{"api.go": "package m\n"})
	if Allowed(bare, "example.com/m") {
		test.Error("Allowed for a module with no go.mod = true, want false: nothing in it asked")
	}
}

// declaredNames answers the package-level names a file declares. A method
// belongs to its receiver's type rather than to the package.
func declaredNames(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				names = append(names, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.ValueSpec:
					for _, ident := range spec.Names {
						names = append(names, ident.Name)
					}
				case *ast.TypeSpec:
					names = append(names, spec.Name.Name)
				}
			}
		}
	}
	return names
}

// checkTree stands in for the build keepCompletion runs, over a module that
// imports nothing: it reads every file of each named package together, which is
// what a compiler does, and a name declared twice is what stops such a module.
func checkTree(root string, pkgs []string) error {
	for _, pkg := range pkgs {
		dir := filepath.Join(root, filepath.FromSlash(pkg))
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		seen := map[string]string{}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, ent.Name()), nil, 0)
			if err != nil {
				return err
			}
			for _, name := range declaredNames(file) {
				if was, dup := seen[name]; dup {
					return fmt.Errorf("%s redeclared in %s: declared in %s", name, ent.Name(), was)
				}
				seen[name] = ent.Name()
			}
		}
	}
	return nil
}

// A generator can write a name the module already declares under another file
// name, which is github.com/charmbracelet/x/ansi: it ships a table it builds at
// run time, and its generator writes a precomputed one beside it. Both declare
// Table, so the completed package compiles for nobody. The module stays as
// published instead, and the added file is not kept.
func TestARedeclaringAdditionIsDiscarded(test *testing.T) {
	const shipped = "package parser\n\ntype TransitionTable []byte\n\nvar Table = GenerateTransitionTable()\n"
	const generated = "package parser\n\n// Code generated by gen.go. DO NOT EDIT.\n\nvar Table = TransitionTable{1}\n"
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
		"parser/transition_table.go": shipped,
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
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
	keep, why, err := keepCompletion(modroot, stage, "example.com/ansi", added, checkTree)
	if err != nil {
		test.Fatalf("keepCompletion: %v", err)
	}
	if keep {
		test.Fatal("the completion was kept, and the package it completes declares Table twice")
	}
	if !strings.Contains(why, "Table") {
		test.Errorf("the rejection said %q, want it to name the declaration a consumer stops at", why)
	}
	if _, err := os.Stat(filepath.Join(modroot, "parser", "table.go")); !errors.Is(err, fs.ErrNotExist) {
		test.Errorf("the module holds the generated file: %v", err)
	}
}

// The feature is what a module that generates part of its own API needs, so a
// completion the package compiles with is kept whole.
func TestACompilingAdditionIsKept(test *testing.T) {
	const shipped = "package parser\n\ntype TransitionTable []byte\n\nvar Table = GenerateTransitionTable()\n"
	const generated = "package parser\n\n// Code generated by gen.go. DO NOT EDIT.\n\nfunc GenerateTransitionTable() TransitionTable { return TransitionTable{1} }\n"
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
		"parser/transition_table.go": shipped,
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
		"parser/transition_table.go": shipped,
		"parser/generated.go":        generated,
	})

	added, err := additions(modroot, stage)
	if err != nil {
		test.Fatal(err)
	}
	keep, why, err := keepCompletion(modroot, stage, "example.com/ansi", added, checkTree)
	if err != nil {
		test.Fatalf("keepCompletion: %v", err)
	}
	if !keep {
		test.Fatalf("the completion was discarded, saying %q", why)
	}
}

// A package that does not compile without the added files is not one the
// completion broke. It keeps them, which is also how a host that cannot build
// at all reads: neither takes a module's generated API away from it.
func TestAPackageBrokenOnItsOwnKeepsItsAdditions(test *testing.T) {
	const shipped = "package parser\n\nvar Table = 1\n\nvar Table = 2\n"
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
		"parser/transition_table.go": shipped,
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/ansi\n",
		"parser/transition_table.go": shipped,
		"parser/generated.go":        "package parser\n\nvar Names = []string{\"a\"}\n",
	})

	added, err := additions(modroot, stage)
	if err != nil {
		test.Fatal(err)
	}
	keep, why, err := keepCompletion(modroot, stage, "example.com/ansi", added, checkTree)
	if err != nil {
		test.Fatalf("keepCompletion: %v", err)
	}
	if !keep {
		test.Fatalf("the completion was discarded, saying %q: the module does not compile without it either", why)
	}
}

// The packages a completion can break are the ones its Go files joined.
func TestAddedPackagesNamesTheDirectoriesOfAddedGoFiles(test *testing.T) {
	added := []string{"api.gen.go", "parser/table.go", "parser/seq.go", "data/table.json", "internal/deep/zz.go"}
	got := addedPackages(added)
	want := []string{".", "internal/deep", "parser"}
	if !slices.Equal(got, want) {
		test.Errorf("addedPackages = %v, want %v", got, want)
	}
}
