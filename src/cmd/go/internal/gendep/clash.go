// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// A module can keep a generator for output it stopped shipping. Its package
// declares those names from source the zip carries, so adding what the
// generator writes redeclares them and the package stops compiling. Every
// consumer of that module then fails on a package none of them named.
//
// So an addition is dropped when the package it lands in already declares one
// of its names. A module that ships the declaration has published it.

// redeclaring answers the added files whose names the module already declares,
// relative to the root and sorted, out of the additions under stage.
func redeclaring(modroot, stage string, added []string) []string {
	var clashing []string
	for dir, files := range goFilesByDir(added) {
		own := declaredIn(filepath.Join(modroot, filepath.FromSlash(dir)))
		if len(own) == 0 {
			continue
		}
		for _, rel := range files {
			names := declaredBy(filepath.Join(stage, filepath.FromSlash(rel)))
			if slices.ContainsFunc(names, func(n string) bool { return own[n] }) {
				clashing = append(clashing, rel)
			}
		}
	}
	slices.Sort(clashing)
	return clashing
}

// goFilesByDir groups the Go files among rels by the directory they sit in.
func goFilesByDir(rels []string) map[string][]string {
	byDir := make(map[string][]string)
	for _, rel := range rels {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		dir := path.Dir(rel)
		byDir[dir] = append(byDir[dir], rel)
	}
	return byDir
}

// declaredIn answers every package-level name the Go files directly in dir
// declare. A file it cannot parse contributes nothing.
func declaredIn(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]bool)
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		for _, name := range declaredBy(filepath.Join(dir, ent.Name())) {
			names[name] = true
		}
	}
	return names
}

// declaredBy answers the package-level names the file at path declares. A
// method is left out: its name is scoped to its receiver.
func declaredBy(path string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, id := range s.Names {
						names = append(names, id.Name)
					}
				}
			}
		}
	}
	return names
}
