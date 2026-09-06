// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ape describes the container the cosmo linker writes.
//
// An Actually Portable Executable is a polyglot header followed by one or
// two ELF images, one per architecture. A reader that wants the ELF has to
// know where the header ends, and that is all this package says.
package ape

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Magic opens every APE, at offset 0, where a DOS header and a shell
// assignment overlap. The linker writes it in cmd/link/internal/ld/ape.go.
const Magic = "MZqFpD='"

// HeaderSize is the size of that header, and so the file offset of the
// first payload. The linker lays every payload out from here
// (layoutAPE), on 64K boundaries.
const HeaderSize = 65536

// Payload returns a reader over the first ELF image in an APE, or nil
// when r does not start with Magic.
//
// The first image is the amd64 one whenever the file has one, and a
// single-architecture APE has only that image. A payload's program
// headers carry absolute file offsets into the APE, so read an image
// through this reader by its SECTIONS: those offsets stay relative to
// the image, which is what a symbol table or DWARF reader asks for.
func Payload(r io.ReaderAt) io.ReaderAt {
	var head [len(Magic)]byte
	if _, err := r.ReadAt(head[:], 0); err != nil || string(head[:]) != Magic {
		return nil
	}
	// The length bounds the section reader, not the file: an image that
	// ends before it is read to the end reports io.EOF either way.
	return io.NewSectionReader(r, HeaderSize, 1<<62)
}

// sidecars are the unstripped ELF images the linker writes beside a fat
// APE, in the order to prefer them. A default build strips the APE
// itself, so these hold the section headers, the symbol table and the
// DWARF.
var sidecars = []string{".dbg", ".aarch64.elf"}

// Sidecar returns the file to read an APE's ELF structure from, or ""
// when name is not an APE or nothing is beside it.
//
// A stripped APE carries its loadable span and nothing else, so a reader
// that wants sections has to read the image the build wrote next to it.
// That is the image gdb and delve already read.
func Sidecar(name string) string {
	if !IsAPE(name) {
		return ""
	}
	// A sidecar named for another sidecar is not a thing the linker
	// writes, so stop rather than chain.
	for _, suffix := range sidecars {
		if strings.HasSuffix(name, suffix) {
			return ""
		}
	}
	for _, suffix := range sidecars {
		if side := name + suffix; regularFile(side) {
			return side
		}
	}
	// The sidecars sit next to the file the build named, which is not
	// this path when a caller copied the APE somewhere else.
	base := filepath.Base(name)
	for _, suffix := range sidecars {
		if side := filepath.Join(filepath.Dir(name), base+suffix); regularFile(side) {
			return side
		}
	}
	return ""
}

func regularFile(name string) bool {
	st, err := os.Stat(name)
	return err == nil && !st.IsDir()
}

// IsAPE reports whether the file at name starts with Magic.
func IsAPE(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [len(Magic)]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false
	}
	return string(head[:]) == Magic
}
