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
// Only the gc linker reads a sourcedir line, so gccgo keeps the directory. A
// package whose compiler flags carry their own -trimpath keeps it too: those
// flags name its source, and the caller reads the names they asked for.
func compilesFromModuleDir(pkg *load.Package) bool {
	if pkg == nil || pkg.Goroot || compilesFromModuleCache(pkg) || cfg.BuildToolchainName != "gc" {
		return false
	}
	if len(pkg.CgoFiles)+len(pkg.SwigFiles)+len(pkg.SwigCXXFiles) > 0 {
		return false
	}
	if setsTrimpath(forcedGcflags) || setsTrimpath(pkg.Internal.Gcflags) {
		return false
	}
	_, _, ok := moduleSourceRoot(pkg)
	return ok
}

// setsTrimpath reports whether flags hand the compiler a -trimpath of their
// own.
func setsTrimpath(flags []string) bool {
	for _, flag := range flags {
		// The flag package reads one dash or two, and a value after '='.
		name, _, _ := strings.Cut(flag, "=")
		if name == "-trimpath" || name == "--trimpath" {
			return true
		}
	}
	return false
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

// SourceDirsID answers a hash of the module directories a link writes into
// its binary for the packages that compiles built. A binary from another
// checkout names other files for them, so a result one of them produced is
// not the result of the other.
func SourceDirsID(compiles []*Action) string {
	hash := cache.NewHash("sourceDirs")
	io.WriteString(hash, sourceDirLines(compiles))
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

// trimSourceDir rewrites each file position in a tool's output that names a
// file in the module directory of pkg to name it by module path. The compiler
// names a file as the go command passed it until it has written its export
// data. The cache must not keep that name for a checkout at another path.
func trimSourceDir(out string, pkg *load.Package) string {
	if pkg == nil || !trimsModuleDirPath(pkg) {
		return out
	}
	name, dir, _ := moduleSourceRoot(pkg)
	return rewritePositionRoot(out, dir+string(filepath.Separator), name+"/", filepath.Separator, '/')
}

// restoreSourceDir rewrites each file position in a tool's output that names
// a file by the module path of pkg to name the file on disk. It undoes
// trimSourceDir, and it restores a position the compiler wrote trimmed. A
// position can be in another package of the module: the compiler reports an
// inlined call where its body is.
func restoreSourceDir(out string, pkg *load.Package) string {
	if pkg == nil || !trimsModuleDirPath(pkg) {
		return out
	}
	name, dir, _ := moduleSourceRoot(pkg)
	return rewritePositionRoot(out, name+"/", dir+string(filepath.Separator), '/', filepath.Separator)
}

// rewritePositionRoot replaces the root before with after in each file
// position of out, and the separator of the path below it. Both roots end in
// their separator. Text that names the root and is no position stays as it
// is: an import path reads the same as a trimmed directory.
func rewritePositionRoot(out, before, after string, beforeSeparator, afterSeparator rune) string {
	if !strings.Contains(out, before) {
		return out
	}
	var rewritten strings.Builder
	from := 0
	for {
		at := strings.Index(out[from:], before)
		if at < 0 {
			break
		}
		at += from
		end := at + len(before)
		rewritten.WriteString(out[from:at])
		from = end
		length := positionPathLength(out[end:])
		if length == 0 || !startsWord(out, at) {
			rewritten.WriteString(before)
			continue
		}
		rewritten.WriteString(after)
		for _, char := range out[end : end+length] {
			if char == beforeSeparator {
				char = afterSeparator
			}
			rewritten.WriteRune(char)
		}
		from = end + length
	}
	rewritten.WriteString(out[from:])
	return rewritten.String()
}

// startsWord reports whether offset at in text begins a line or follows a
// space, a tab or an opening parenthesis.
func startsWord(text string, at int) bool {
	if at == 0 {
		return true
	}
	before := text[at-1]
	return before == '\n' || before == ' ' || before == '\t' || before == '('
}

// positionPathLength answers the length of the file path that text begins
// with, when a colon and a line number follow it: the rest of a position
// whose root came before it. It answers zero for anything else. An import
// path has no line number. A path with an '@' is a module of the module
// cache that sits under the path of this one.
func positionPathLength(text string) int {
	for at := 0; at < len(text); at++ {
		switch text[at] {
		case ' ', '\t', '\n', '@':
			return 0
		case ':':
			if at > 0 && at+1 < len(text) && '0' <= text[at+1] && text[at+1] <= '9' {
				return at
			}
			return 0
		}
	}
	return 0
}
