// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gendep

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// A generator can write a file the module already ships under another name.
// charmbracelet/x/ansi does: gen.go writes parser/table.go, and the module
// carries the same Table in parser/transition_table.go. Adding that file gives
// the package two declarations of one name, so the completed module compiles
// nowhere.

// withoutRedeclarations drops each added Go file that declares a name the
// module's own files in that directory already declare. The dropped names are
// reported on stderr: the package builds from what the module ships, which is
// what it did before the generator ran.
func withoutRedeclarations(modroot, stage, mod string, added []string) []string {
	kept := added[:0]
	for _, rel := range added {
		name := declClash(modroot, stage, rel)
		if name == "" {
			kept = append(kept, rel)
			continue
		}
		fmt.Fprintf(os.Stderr, "go: %s generated %s, which redeclares %s: keeping what the module ships\n", mod, rel, name)
	}
	return kept
}

// declClash answers a top-level name the staged file rel declares that a file
// the module itself carries in the same directory declares too, or "".
func declClash(modroot, stage, rel string) string {
	if !strings.HasSuffix(rel, ".go") {
		return ""
	}
	generated := topLevelNames(filepath.Join(stage, filepath.FromSlash(rel)))
	if len(generated) == 0 {
		return ""
	}
	dir := filepath.Dir(filepath.Join(modroot, filepath.FromSlash(rel)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") {
			continue
		}
		for _, name := range topLevelNames(filepath.Join(dir, ent.Name())) {
			for _, got := range generated {
				if got == name {
					return name
				}
			}
		}
	}
	return ""
}

// topLevelNames answers the names a Go file declares at package scope. A file
// that does not parse declares nothing here: the build reports that itself.
func topLevelNames(path string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name != "_" && d.Name.Name != "init" {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						if ident.Name != "_" {
							names = append(names, ident.Name)
						}
					}
				}
			}
		}
	}
	return names
}
