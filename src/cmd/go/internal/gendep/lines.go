// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package gendep

import (
	"errors"

	"github.com/wow-look-at-my/go-mmap"
)

// fileLines calls visit with each line of the file at path, as a subslice of
// a read-only mapping. A line costs no copy and has no length limit.
func fileLines(path string, visit func(line []byte) bool) error {
	err := mmap.FileLines(path, visit)
	if errors.Is(err, errors.ErrUnsupported) {
		return readLines(path, visit)
	}
	return err
}
