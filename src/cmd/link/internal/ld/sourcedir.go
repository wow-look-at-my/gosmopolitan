// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import (
	"path/filepath"
	"slices"
	"strings"
)

// The go command compiles a package of the main module with its source named
// by module path, so one object serves every checkout. A "sourcedir" line of
// the import configuration says which directory that module is in for this
// link. The file tables of the binary then name the files on disk.

// sourceDir is one module root: the name a trimmed compile gave it, and the
// directory it is.
type sourceDir struct {
	name string
	dir  string
}

// sourceDirs holds the roots of this link, longest name first. A module that
// a replace directive points at a directory can sit under the path of the
// main module, and its files belong to the longer name.
var sourceDirs []sourceDir

// addSourceDir records that the files a compile named under name are in dir.
func addSourceDir(name, dir string) {
	sourceDirs = append(sourceDirs, sourceDir{name: name, dir: filepath.ToSlash(dir)})
	slices.SortStableFunc(sourceDirs, func(left, right sourceDir) int {
		return len(right.name) - len(left.name)
	})
}

// expandSourceDir answers file with the module name that leads it replaced by
// the directory of that module. A file under no known module comes back as
// it is, which is what every file does in a link with -trimpath.
func expandSourceDir(file string) string {
	for _, root := range sourceDirs {
		below, ok := strings.CutPrefix(file, root.name+"/")
		if !ok {
			continue
		}
		// A module in the module cache can sit under the path of this one.
		// Its name carries a version, and an import path never holds an '@'.
		if last := strings.LastIndexByte(below, '/'); last >= 0 && strings.Contains(below[:last], "@") {
			return file
		}
		return root.dir + "/" + below
	}
	return file
}
