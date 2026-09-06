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
// when r is not one.
//
// Two shapes carry an image. A pristine APE begins with Magic. One that
// has run on a Linux host begins with the ELF header its loader wrote
// over the polyglot one, which describes the payload for EXECUTION and
// names no sections at all. Either shape holds the image itself at
// HeaderSize.
//
// The first image is the amd64 one whenever the file has one, and a
// single-architecture APE has only that image. A payload's program
// headers carry absolute file offsets into the APE, so read an image
// through this reader by its SECTIONS: those offsets stay relative to
// the image, which is what a symbol table or DWARF reader asks for.
func Payload(r io.ReaderAt) io.ReaderAt {
	var head [ehdrSize]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil
	}
	if string(head[:len(Magic)]) != Magic && !assimilated(r, &head) {
		return nil
	}
	// The length bounds the section reader, not the file: an image that
	// ends before it is read to the end reports io.EOF either way.
	return io.NewSectionReader(r, HeaderSize, 1<<62)
}

// ehdrSize is the size of an ELF64 header, which is what an APE carries
// and what its loader writes over the file's own first bytes.
const ehdrSize = 64

// assimilated reports whether head is the ELF header an APE loader
// wrote over the polyglot one.
//
// That header exists to be executed: it names no sections, because a
// payload's section table sits at an offset relative to the payload
// rather than to the file. So the section-less header stands in front
// of an image that has the table, and the two agree on what they are.
// A file that carries both, agreeing, is an assimilated APE.
func assimilated(r io.ReaderAt, head *[ehdrSize]byte) bool {
	if string(head[:4]) != elfMagic || u16(head[60:]) != 0 { // e_shnum
		return false
	}
	var payload [ehdrSize]byte
	if _, err := r.ReadAt(payload[:], HeaderSize); err != nil {
		return false
	}
	if string(payload[:4]) != elfMagic {
		return false
	}
	// e_ident's class and data, e_type, e_machine and e_entry are the
	// fields the loader's header takes from the payload.
	return payload[4] == head[4] && payload[5] == head[5] &&
		u16(payload[16:]) == u16(head[16:]) &&
		u16(payload[18:]) == u16(head[18:]) &&
		u64(payload[24:]) == u64(head[24:])
}

const elfMagic = "\x7fELF"

func u16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }

func u64(b []byte) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
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
