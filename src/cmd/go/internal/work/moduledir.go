// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package work

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"cmd/go/internal/cache"
	"cmd/go/internal/cfg"
	"cmd/go/internal/load"
	"cmd/go/internal/str"
)

// A package of the main module compiles from a checkout, and the checkout
// sits at another path on each machine. Its compile names its source by
// module path, as -trimpath does, so each checkout asks for one key and gets
// one object. The link puts the directory back, so a stack trace still names
// the file on disk.

// trimsModuleDirPath reports whether the compile of pkg names its source by
// module path without -trimpath, and the link restores the directory.
func trimsModuleDirPath(pkg *load.Package) bool {
	return !cfg.BuildTrimpath && compilesFromModuleDir(pkg)
}

// compilesFromModuleDir reports whether pkg sits in the directory of a module
// outside the module cache: the main module, or a module that a replace
// directive points at a directory. A package with cgo or SWIG keeps its
// directory, because the C compiler and the cgo tool write that path too.
// Only the gc linker reads a sourcedir line, so gccgo keeps the directory.
func compilesFromModuleDir(pkg *load.Package) bool {
	if pkg == nil || pkg.Goroot || compilesFromModuleCache(pkg) || cfg.BuildToolchainName != "gc" {
		return false
	}
	if len(pkg.CgoFiles)+len(pkg.SwigFiles)+len(pkg.SwigCXXFiles) > 0 {
		return false
	}
	_, _, ok := moduleSourceRoot(pkg)
	return ok
}

// moduleSourceRoot answers the name a trimmed compile gives the root of the
// module of pkg, and the directory that root is. It answers false when the
// directory of pkg is not the one its import path names under that root. The
// linker maps the name back by prefix, so the two must agree.
func moduleSourceRoot(pkg *load.Package) (name, dir string, ok bool) {
	module := pkg.Module
	if module == nil || module.Dir == "" {
		return "", "", false
	}
	importPath := pkg.Internal.OrigImportPath
	if importPath == "" {
		importPath = pkg.ImportPath
	}
	if !str.HasPathPrefix(importPath, module.Path) {
		return "", "", false
	}
	below := strings.TrimPrefix(importPath, module.Path)
	root := filepath.Clean(module.Dir)
	if filepath.Join(root, filepath.FromSlash(below)) != filepath.Clean(pkg.Dir) {
		return "", "", false
	}
	name = module.Path
	if module.Version != "" {
		name += "@" + module.Version
	}
	return name, root, true
}

// trimmedSourceDir answers the directory a trimmed compile of pkg records in
// place of its directory on disk.
func trimmedSourceDir(pkg *load.Package) string {
	importPath := pkg.Internal.OrigImportPath
	if module := pkg.Module; module != nil && module.Version != "" {
		return module.Path + "@" + module.Version + strings.TrimPrefix(importPath, module.Path)
	}
	return importPath
}

// sourceDirLines answers one "sourcedir name=directory" line for each module
// directory the packages of a link compiled from, sorted. The linker rewrites
// each file name under name to the directory. It answers nothing with
// -trimpath, which keeps every directory out of the binary.
func sourceDirLines(deps []*Action) string {
	if cfg.BuildTrimpath {
		return ""
	}
	var lines []string
	for _, dep := range deps {
		if dep.Package == nil || !compilesFromModuleDir(dep.Package) {
			continue
		}
		name, dir, _ := moduleSourceRoot(dep.Package)
		line := fmt.Sprintf("sourcedir %s=%s\n", name, dir)
		if !slices.Contains(lines, line) {
			lines = append(lines, line)
		}
	}
	slices.Sort(lines)
	return strings.Join(lines, "")
}

// SourceDirsID answers a hash of the module directories the link of
// linkAction writes into its binary. A binary from another checkout names
// other files, so a result one of them produced is not the result of the
// other.
func SourceDirsID(linkAction *Action) string {
	hash := cache.NewHash("sourceDirs")
	io.WriteString(hash, sourceDirLines(linkAction.Deps))
	return fmt.Sprintf("%x", hash.Sum())
}

// namesLocalDir reports whether text names a directory of this machine: the
// module of pkg, the module cache or GOROOT. A cached copy of such text is
// correct only where those directories are the same.
func namesLocalDir(text []byte, pkg *load.Package) bool {
	dirs := []string{pkg.Dir, cfg.GOMODCACHE, cfg.GOROOT}
	if pkg.Module != nil {
		dirs = append(dirs, pkg.Module.Dir)
	}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		// A JSON stream doubles each backslash of a Windows path.
		for _, form := range []string{dir, filepath.ToSlash(dir), strings.ReplaceAll(dir, `\`, `\\`)} {
			if strings.Contains(string(text), form) {
				return true
			}
		}
	}
	return false
}

// trimSourceDir rewrites each mention of the directory of pkg in a tool's
// output to the trimmed directory. The compiler names a file as the go
// command passed it until it has written its export data. The cache must not
// keep that name for a checkout at another path.
func trimSourceDir(out string, pkg *load.Package) string {
	if pkg == nil || !trimsModuleDirPath(pkg) {
		return out
	}
	dir := filepath.Clean(pkg.Dir) + string(filepath.Separator)
	return replacePrefix(out, dir, trimmedSourceDir(pkg)+"/")
}

// restoreSourceDir rewrites each file position in a tool's output that names
// the trimmed directory of pkg to name the directory on disk. The cache keeps
// the output as the tool wrote it, which is the same text in every checkout.
func restoreSourceDir(out string, pkg *load.Package) string {
	if pkg == nil || !trimsModuleDirPath(pkg) {
		return out
	}
	trimmed := trimmedSourceDir(pkg) + "/"
	if !strings.Contains(out, trimmed) {
		return out
	}
	dir := filepath.Clean(pkg.Dir) + string(filepath.Separator)
	var restored strings.Builder
	from := 0
	for {
		at := strings.Index(out[from:], trimmed)
		if at < 0 {
			break
		}
		at += from
		end := at + len(trimmed)
		restored.WriteString(out[from:at])
		if startsWord(out, at) && namesFileLine(out[end:]) {
			restored.WriteString(dir)
		} else {
			restored.WriteString(trimmed)
		}
		from = end
	}
	restored.WriteString(out[from:])
	return restored.String()
}

// startsWord reports whether offset at in text begins a line or follows a
// space or a tab.
func startsWord(text string, at int) bool {
	if at == 0 {
		return true
	}
	before := text[at-1]
	return before == '\n' || before == ' ' || before == '\t'
}

// namesFileLine reports whether text begins with one file name, a colon and
// a line number: the rest of a position whose directory came before it. An
// import path that happens to continue the directory has no line number.
func namesFileLine(text string) bool {
	for at := 0; at < len(text); at++ {
		switch text[at] {
		case '/', '\\', ' ', '\t', '\n':
			return false
		case ':':
			return at > 0 && at+1 < len(text) && '0' <= text[at+1] && text[at+1] <= '9'
		}
	}
	return false
}
