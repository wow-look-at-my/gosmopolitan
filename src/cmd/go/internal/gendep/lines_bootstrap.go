// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cmd_go_bootstrap || js || wasip1

package gendep

// fileLines reads the file whole. go_bootstrap builds without go-mmap and its
// x/sys dependency. js and wasip1 map no memory.
func fileLines(path string, visit func(line []byte) bool) error {
	return readLines(path, visit)
}
