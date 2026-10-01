// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements the Import function for tests to use gc-generated object files.

package importer

import (
	"bufio"
	"fmt"
	"internal/exportdata"
	"internal/pkgbits"
	"io"
	"os"

	"cmd/compile/internal/types2"
)

// Import imports a gc-generated package given its import path and srcDir, adds
// the corresponding package object to the packages map, and returns the object.
// The packages map must contain all packages already imported.
//
// This function should only be used in tests.
func Import(packages map[string]*types2.Package, path, srcDir string, lookup func(path string) (io.ReadCloser, error)) (pkg *types2.Package, err error) {
	var rc io.ReadCloser
	var id string
	if lookup != nil {
		// With custom lookup specified, assume that caller has
		// converted path to a canonical import path for use in the map.
		if path == "unsafe" {
			return types2.Unsafe, nil
		}
		id = path

		// No need to re-import if the package was imported completely before.
		if pkg = packages[id]; pkg != nil && pkg.Complete() {
			return
		}
		// The sibling branch names the file it could not open. This one
		// hands back whatever the caller's lookup said, so a lookup that
		// answers a bare os.ErrInvalid reaches types2 as the two words
		// "invalid argument", with nothing to take them to.
		f, err := lookup(path)
		if err != nil {
			return nil, fmt.Errorf("the lookup for %s: %w", path, err)
		}
		rc = f
	} else {
		var filename string
		filename, id, err = exportdata.FindPkg(path, srcDir)
		if filename == "" {
			if path == "unsafe" {
				return types2.Unsafe, nil
			}
			return nil, err
		}

		// no need to re-import if the package was imported completely before
		if pkg = packages[id]; pkg != nil && pkg.Complete() {
			return
		}

		// open file
		f, err := os.Open(filename)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil {
				// add file name to error
				err = fmt.Errorf("%s: %v", filename, err)
			}
		}()
		rc = f
	}
	defer rc.Close()

	buf := bufio.NewReader(rc)
	data, err := exportdata.ReadUnified(buf)
	if err != nil {
		err = fmt.Errorf("import %q: %v", path, err)
		return
	}
	s := string(data)

	input := pkgbits.NewPkgDecoder(id, s)
	pkg = ReadPackage(nil, packages, input)

	return
}
