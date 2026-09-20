// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"slices"
	"testing"
)

// github.com/charmbracelet/x/ansi is the module this exists for. It builds its
// transition table at run time and keeps the generator that writes a
// precomputed one beside it. Adding that file declares Table twice, and every
// consumer of the module fails to compile a package it never named.
func TestAGeneratorsRedeclaringFileIsDropped(test *testing.T) {
	const shipped = "package parser\n\ntype TransitionTable []byte\n\nvar Table = GenerateTransitionTable()\n"
	const generated = "package parser\n\nvar Table = TransitionTable{1}\n"
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"parser/transition_table.go": shipped,
		"parser/table.go":            generated,
	})

	dropped := redeclaring(modroot, stage, []string{"parser/table.go"})
	if !slices.Equal(dropped, []string{"parser/table.go"}) {
		test.Fatalf("redeclaring = %v, want the file that declares Table twice", dropped)
	}
}

// The case this exists for is a module that already declares the name. A
// generated file that declares something new is the whole point of completing
// a module, so nothing may drop it.
func TestAnOrdinaryGeneratedFileIsKept(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"data/data.go": "package data\n\nfunc Name() string { return \"\" }\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"data/data.go":      "package data\n\nfunc Name() string { return \"\" }\n",
		"data/table_gen.go": "package data\n\nvar Table = []byte{1}\n",
	})

	if dropped := redeclaring(modroot, stage, []string{"data/table_gen.go"}); len(dropped) != 0 {
		test.Fatalf("redeclaring = %v, want nothing dropped", dropped)
	}
}

// A method's name is scoped to its receiver, so two types may both have one.
// Reading a method as a package-level name drops a generated file that
// compiles.
func TestAMethodIsNotAPackageLevelName(test *testing.T) {
	modroot := writeTree(test, test.TempDir(), map[string]string{
		"data/data.go": "package data\n\ntype A struct{}\n\nfunc (A) String() string { return \"\" }\n",
	})
	stage := writeTree(test, test.TempDir(), map[string]string{
		"data/data.go":  "package data\n\ntype A struct{}\n\nfunc (A) String() string { return \"\" }\n",
		"data/b_gen.go": "package data\n\ntype B struct{}\n\nfunc (B) String() string { return \"\" }\n",
	})

	if dropped := redeclaring(modroot, stage, []string{"data/b_gen.go"}); len(dropped) != 0 {
		test.Fatalf("redeclaring = %v, want nothing dropped", dropped)
	}
}
