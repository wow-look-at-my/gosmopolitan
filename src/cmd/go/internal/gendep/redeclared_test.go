// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"slices"
	"testing"
)

// charmbracelet/x/ansi ships the parser table as transition_table.go and keeps
// a generator that writes table.go. Adding that file declares Table twice, so
// the module compiles nowhere. What the module ships is what the build keeps.
func TestAGeneratedFileThatRedeclaresTheModulesOwnNameIsDropped(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/m\n",
		"parser/transition_table.go": "package parser\n\nvar Table = []int{1}\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":                     "module example.com/m\n",
		"parser/transition_table.go": "package parser\n\nvar Table = []int{1}\n",
		"parser/table.go":            "package parser\n\nvar Table = []int{2}\n",
		"parser/extra.go":            "package parser\n\nfunc Extra() {}\n",
	})

	got := withoutRedeclarations(modroot, stage, "example.com/m", []string{"parser/extra.go", "parser/table.go"})
	want := []string{"parser/extra.go"}
	if !slices.Equal(got, want) {
		test.Errorf("withoutRedeclarations = %q, want %q", got, want)
	}
}

// A method declares no package-scope name, so a generated file of methods on a
// type the module ships is kept.
func TestAGeneratedMethodIsNotARedeclaration(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"go.mod": "module example.com/m\n",
		"api.go": "package m\n\ntype T struct{}\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"go.mod":          "module example.com/m\n",
		"api.go":          "package m\n\ntype T struct{}\n",
		"zz_generated.go": "package m\n\nfunc (T) String() string { return \"\" }\n",
	})

	got := withoutRedeclarations(modroot, stage, "example.com/m", []string{"zz_generated.go"})
	want := []string{"zz_generated.go"}
	if !slices.Equal(got, want) {
		test.Errorf("withoutRedeclarations = %q, want %q", got, want)
	}
}
