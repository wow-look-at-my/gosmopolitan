// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package objfile

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// apeMagic opens every Actually Portable Executable. The linker writes
// it at offset 0 (cmd/link/internal/ld/ape.go), where a DOS header and
// a shell assignment overlap.
const apeMagic = "MZqFpD='"

// apeSidecars are the unstripped ELF images the linker writes beside a
// fat APE, in the order to prefer them. A default build strips the APE
// itself, so these hold the symbol table and the DWARF.
var apeSidecars = []string{".dbg", ".aarch64.elf"}

// apeDebugFile returns the sidecar to read symbols from for the APE at
// name, or "" when name is not a stripped APE or no sidecar is beside
// it.
//
// A stripped APE has no runtime.pclntab, so every tool over this
// package reports it as an unrecognized or symbol-less object. The
// symbols are one file away, and gdb and delve already read them from
// there, so these tools read them from there too.
func apeDebugFile(name string) string {
	if !isAPE(name) {
		return ""
	}
	// A sidecar named for another sidecar is not a thing the linker
	// writes, so stop rather than chain.
	for _, suffix := range apeSidecars {
		if strings.HasSuffix(name, suffix) {
			return ""
		}
	}
	for _, suffix := range apeSidecars {
		side := name + suffix
		if st, err := os.Stat(side); err == nil && !st.IsDir() {
			return side
		}
	}
	// The sidecars sit next to the file the build named, which is not
	// this path when a caller copied the APE somewhere else.
	base := filepath.Base(name)
	for _, suffix := range apeSidecars {
		side := filepath.Join(filepath.Dir(name), base+suffix)
		if st, err := os.Stat(side); err == nil && !st.IsDir() {
			return side
		}
	}
	return ""
}

// isAPE reports whether the file at name starts with the APE magic.
func isAPE(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [len(apeMagic)]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false
	}
	return string(head[:]) == apeMagic
}
