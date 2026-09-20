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
	"sort"
	"strings"

	"cmd/go/internal/imports"
)

// A generator's output is not always the half a package is missing. It is
// sometimes an ALTERNATIVE to a file the module ships. x/ansi ships a table it
// builds at run time and generates a precomputed one beside it, and both
// declare Table. A tree that holds the two compiles as neither, and a consumer
// has no way to drop one. The published bytes are the module, so the generated
// file is what goes.

// superseded answers the added files a package drops, because one of them
// declares a name the module's own files declare there already. Every addition
// in such a package goes: a package that keeps some of what its generator wrote
// compiles against a half it never finished.
//
// The added files are read from stage, where the generator wrote them, and the
// module's own from modroot. Only files that build everywhere are read, because
// two platform files declare one name all the time and no build reads them
// together.
func superseded(modroot, stage string, added []string) []string {
	byDir := map[string][]string{}
	for _, rel := range added {
		if strings.HasSuffix(rel, ".go") {
			byDir[path.Dir(rel)] = append(byDir[path.Dir(rel)], rel)
		}
	}
	drop := map[string]bool{}
	for dir, rels := range byDir {
		own := ownNames(filepath.Join(modroot, filepath.FromSlash(dir)))
		if len(own) == 0 || !anyRedeclares(stage, rels, own) {
			continue
		}
		for _, rel := range added {
			if path.Dir(rel) == dir {
				drop[rel] = true
			}
		}
	}
	var out []string
	for rel := range drop {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// anyRedeclares reports that one of rels, under root, declares a name own holds.
func anyRedeclares(root string, rels []string, own map[string]bool) bool {
	for _, rel := range rels {
		for name := range declaredNames(filepath.Join(root, filepath.FromSlash(rel))) {
			if own[name] {
				return true
			}
		}
	}
	return false
}

// ownNames answers the package-level names the files in dir declare.
func ownNames(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		for name := range declaredNames(filepath.Join(dir, e.Name())) {
			out[name] = true
		}
	}
	return out
}

// declaredNames answers the package-level names a file declares, or nothing
// when the file does not build everywhere, does not parse, or is a test.
// A method is not a package-level name and is left out.
func declaredNames(file string) map[string]bool {
	name := filepath.Base(file)
	if strings.HasSuffix(name, "_test.go") || !imports.MatchFile(name, nil) {
		return nil
	}
	src, err := os.ReadFile(file)
	if err != nil || !imports.ShouldBuild(src, nil) {
		return nil
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, id := range s.Names {
						out[id.Name] = true
					}
				}
			}
		}
	}
	return out
}
