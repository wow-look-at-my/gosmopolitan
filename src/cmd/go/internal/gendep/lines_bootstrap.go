// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap

package gendep

import (
	"bytes"
	"os"
)

// fileLines reads the file whole. go_bootstrap builds without go-mmap and its
// x/sys dependency, and it never fetches a module to scan.
func fileLines(path string, visit func(line []byte) bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for line := range bytes.Lines(data) {
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
		if !visit(line) {
			return nil
		}
	}
	return nil
}
