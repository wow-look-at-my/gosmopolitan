// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/sys"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// apeFatMerge implements the -apefat linker mode: it assembles the given
// GOOS=cosmo binaries (at most one per architecture; each either an APE
// produced by this linker or a raw ELF) into a single APE at outfile,
// skipping normal linking entirely. Two inputs give a fat APE; one input
// re-emits a single-architecture APE, so a build restricted to one
// architecture still gets a fat build's stripping, sidecars and header.
//
// With -apedbg each input's pristine ELF goes to a sidecar beside
// outfile; with -apestrip each payload is then cut to the span its
// program headers reference. -apedbgmode selects how much the sidecars
// carry (apedebug.go); work.cosmoMergeArgs decides when cmd/go passes
// these flags.
func apeFatMerge(spec, outfile string) {
	if outfile == "" {
		Exitf("-apefat requires -o")
	}
	switch *flagApeDbgMode {
	case "full":
	case "slim":
		if !*flagApeDbg {
			Exitf("-apedbgmode=slim requires -apedbg")
		}
	case "compact":
		if !*flagApeDbg || !*flagApeStrip {
			Exitf("-apedbgmode=compact requires -apedbg and -apestrip")
		}
	default:
		Exitf("-apedbgmode: invalid mode %q (valid: full, slim, compact)", *flagApeDbgMode)
	}
	inputs := strings.Split(spec, ",")
	if len(inputs) != 1 && len(inputs) != 2 {
		Exitf("-apefat requires one or two cosmo inputs, got %d", len(inputs))
	}
	var payloads []*apePayload
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			Exitf("-apefat: %v", err)
		}
		p, err := payloadFromAPEOrELF(data)
		if err != nil {
			Exitf("-apefat: %s: %v", in, err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) == 2 {
		if payloads[0].arch == payloads[1].arch {
			Exitf("-apefat: inputs must be different architectures")
		}
		// Canonical layout: amd64 image first, so the Mach-O header
		// references the payload right after the APE header.
		if payloads[0].arch != sys.AMD64 {
			payloads[0], payloads[1] = payloads[1], payloads[0]
		}
	}
	// Compact mode reads each payload's pristine image (section table,
	// symtab, DWARF) after stripping has removed it from p.elf, so copy
	// first: stripPayload re-slices the same backing array and zeroes
	// header fields in place.
	var pristine [][]byte
	if *flagApeDbgMode == "compact" {
		for _, p := range payloads {
			pristine = append(pristine, append([]byte(nil), p.elf...))
		}
	}
	if *flagApeDbg {
		for _, p := range payloads {
			writeAPEDebugSidecar(outfile, p)
		}
	}
	if *flagApeStrip {
		for _, p := range payloads {
			stripPayload(p)
		}
	}
	var tail []byte
	var tailOff uint64
	if *flagApeDbgMode == "compact" {
		tail, tailOff = apeCompactDebugTail(payloads, pristine)
	}
	writeAPEFile(outfile, payloads)
	if tail != nil {
		appendAPEFileTail(outfile, tailOff, tail)
	}
}

// apeCompactDebugTail builds the compact debug tail for the payloads (in
// their final order) and patches each payload's ELF header to reference
// its section-header view by absolute file offset - both in the stored
// payload and, via makeEmbeddedElfHeader's propagation, in the boot
// header that self-assimilation writes over the file's first 64 bytes.
// The tail lands past the last payload's end (8-aligned), outside every
// loadable span: it is never mapped at runtime, and every APE boot path
// reads only ELF and program headers, so execution is unaffected.
func apeCompactDebugTail(payloads []*apePayload, pristine [][]byte) (tail []byte, tailOff uint64) {
	layoutAPE(payloads) // same deterministic layout writeAPEFile recomputes
	last := payloads[len(payloads)-1]
	end := last.offset + uint64(len(last.elf))
	// The PE image's rounded .data tail is written after the last payload
	// (see apePEFileEnd), so the debug tail has to start past it.
	if pe := apePEFileEnd(payloads); pe > end {
		end = pe
	}
	tailOff = (end + 7) &^ uint64(7)
	for i, p := range payloads {
		var view apeCompactView
		var err error
		tail, view, err = appendCompactDebugView(tail, tailOff, pristine[i], p.offset)
		if err != nil {
			Exitf("-apedbgmode=compact: %s payload: %v", p.arch, err)
		}
		binary.LittleEndian.PutUint64(p.elf[40:48], view.shoff)    // e_shoff
		binary.LittleEndian.PutUint16(p.elf[60:62], view.shnum)    // e_shnum
		binary.LittleEndian.PutUint16(p.elf[62:64], view.shstrndx) // e_shstrndx
	}
	return tail, tailOff
}

// appendAPEFileTail appends tail to the APE at outfile so that its first
// byte lands at file offset tailOff, zero-padding the gap from the
// current end of file.
func appendAPEFileTail(outfile string, tailOff uint64, tail []byte) {
	f, err := os.OpenFile(outfile, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		Exitf("-apedbgmode=compact: %v", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		Exitf("-apedbgmode=compact: %v", err)
	}
	if uint64(fi.Size()) > tailOff {
		Exitf("-apedbgmode=compact: APE ends at %#x, past the debug tail offset %#x", fi.Size(), tailOff)
	}
	if pad := tailOff - uint64(fi.Size()); pad > 0 {
		if _, err := f.Write(make([]byte, pad)); err != nil {
			Exitf("-apedbgmode=compact: %v", err)
		}
	}
	if _, err := f.Write(tail); err != nil {
		Exitf("-apedbgmode=compact: %v", err)
	}
}

// apeDebugSidecarName returns the debug sidecar path for a payload of the
// given architecture next to the APE at outfile. The names follow the
// Cosmopolitan cosmocc convention, which cosmo libc's FindDebugBinary
// probes at crash time by appending each extension to the executable name:
// <outfile>.dbg for the amd64 image, <outfile>.aarch64.elf for arm64.
func apeDebugSidecarName(outfile string, arch sys.ArchFamily) string {
	if arch == sys.ARM64 {
		return outfile + ".aarch64.elf"
	}
	return outfile + ".dbg"
}

// writeAPEDebugSidecar writes payload p's debug sidecar for its
// architecture. In the default -apedbgmode=full it is p's ELF image exactly
// as its linker produced it (p_offset values payload-relative, symbol table
// and DWARF intact): a complete standalone ELF executable, directly
// loadable by debuggers. In slim and compact modes the image is first
// reduced to its debug-only form (see slimELFDebug): same DWARF and symbol
// table, allocated section contents dropped, not runnable.
func writeAPEDebugSidecar(outfile string, p *apePayload) {
	name := apeDebugSidecarName(outfile, p.arch)
	img := p.elf
	if *flagApeDbgMode != "full" {
		slim, err := slimELFDebug(img)
		if err != nil {
			Exitf("-apedbgmode=%s: %s: %v", *flagApeDbgMode, name, err)
		}
		img = slim
	}
	if err := os.WriteFile(name, img, 0755); err != nil {
		Exitf("-apedbg: %v", err)
	}
}

// payloadExtent returns the end of the file span referenced by the ELF
// image's program headers: max over all entries of p_offset+p_filesz, but
// no less than the end of the program header table itself. Everything past
// it is non-loadable content (.debug_* sections, .symtab, .strtab, and the
// section header table). The image must already have passed payloadFromELF
// validation.
func payloadExtent(elf []byte) uint64 {
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phentsize := binary.LittleEndian.Uint16(elf[54:56])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	extent := phoff + uint64(phnum)*uint64(phentsize)
	for i := uint16(0); i < phnum; i++ {
		ph := elf[phoff+uint64(i)*uint64(phentsize):]
		off := binary.LittleEndian.Uint64(ph[8:16])
		filesz := binary.LittleEndian.Uint64(ph[32:40])
		if end := off + filesz; end > extent {
			extent = end
		}
	}
	return extent
}

// stripPayload cuts p's ELF image down to the span its program headers
// reference and zeroes the ELF header's section fields (e_shoff, e_shnum,
// e_shstrndx), which no longer point at anything. Every APE boot path -
// the embedded boot headers, self-assimilation, the Mach-O header, and the
// macOS ARM64 APE loader - reads only the ELF and program headers, so the
// stripped image boots exactly like the full one.
func stripPayload(p *apePayload) {
	extent := payloadExtent(p.elf)
	if extent > uint64(len(p.elf)) {
		Exitf("-apestrip: program headers reference %#x bytes but image has only %#x", extent, len(p.elf))
	}
	elf := p.elf[:extent:extent]
	binary.LittleEndian.PutUint64(elf[40:48], 0) // e_shoff
	binary.LittleEndian.PutUint16(elf[60:62], 0) // e_shnum
	binary.LittleEndian.PutUint16(elf[62:64], 0) // e_shstrndx
	p.elf = elf
}

// payloadFromAPEOrELF extracts an APE payload from data, which may be a raw
// ELF image or an APE file produced by this linker (whose single payload
// lives at apeHeaderSize with p_offset values shifted by apeHeaderSize).
func payloadFromAPEOrELF(data []byte) (*apePayload, error) {
	if len(data) > apeHeaderSize+64 && string(data[0:7]) == "MZqFpD=" {
		// Validate before shiftPOffsets touches the program headers.
		p, err := payloadFromELF(data[apeHeaderSize:])
		if err != nil {
			return nil, err
		}
		if hasSecondAPEPayload(data) {
			return nil, fmt.Errorf("input is already a fat APE; pass the original single-arch binaries")
		}
		delta := uint64(apeHeaderSize)
		p.elf = shiftPOffsets(p.elf, -delta) // unsigned wraparound subtracts
		// Keep the input's APE head: for an amd64 input it carries the
		// real PE header its thin link computed, which the fat header
		// transplants verbatim (see transplantPEHeader).
		p.head = data[:apeHeaderSize:apeHeaderSize]
		return p, nil
	}
	return payloadFromELF(data)
}

// hasSecondAPEPayload reports whether the APE file data contains another ELF
// image beyond the extent of the first payload at apeHeaderSize, meaning it
// is already a fat APE. Merging such a file would silently ingest the first
// payload's slice spanning both images, so apeFatMerge rejects it. The first
// payload must already have passed payloadFromELF validation. layoutAPE
// places every additional payload at an apePayloadAlign boundary at or after
// the previous image's end, so scanning aligned offsets beyond the first
// image's segments finds it.
func hasSecondAPEPayload(data []byte) bool {
	elf := data[apeHeaderSize:]
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	extent := uint64(apeHeaderSize + 64)
	for i := uint16(0); i < phnum; i++ {
		ph := elf[phoff+uint64(i)*56:]
		// p_offset values in a stored APE are absolute file offsets.
		off := binary.LittleEndian.Uint64(ph[8:16])
		filesz := binary.LittleEndian.Uint64(ph[32:40])
		end := off + filesz
		if end < off || end > uint64(len(data)) {
			end = uint64(len(data))
		}
		if end > extent {
			extent = end
		}
	}
	for off := (extent + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1); off+4 <= uint64(len(data)); off += apePayloadAlign {
		if string(data[off:off+4]) == elfMagic {
			return true
		}
	}
	return false
}
