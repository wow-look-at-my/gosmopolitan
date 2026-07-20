// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

// Debug-info reduction for GOOS=cosmo fat APE merges (the -apedbgmode
// linker flag, driven by cmd/go's GOCOSMODEBUG):
//
//   - "full" (default): sidecars are pristine byte-copies of the per-arch
//     linker ELFs; no transform runs. Behavior is identical to before the
//     flag existed.
//   - "slim": sidecars are debug-only ELFs, the in-linker equivalent of
//     objcopy --only-keep-debug: contents of allocated sections are
//     dropped (their headers become SHT_NOBITS with addresses preserved),
//     while .symtab, .strtab, and every .debug_* section keep their bytes.
//     About two thirds of a pristine sidecar duplicates bytes already
//     shipped inside the APE; slim removes exactly that duplication.
//   - "compact": slim sidecars, plus a compact debug view appended to the
//     APE itself past the loadable span (never mapped at runtime), so
//     debuggers can symbolize the assimilated binary with no sidecar
//     present. See apeCompactDebugTail.
//
// The transforms operate on the raw ELF images byte-wise, in the style of
// the rest of the APE writer (no debug/elf dependency).

import (
	"encoding/binary"
	"fmt"
)

// Elf64 section header constants used by the debug-view transforms.
const (
	elfShtNull     = 0
	elfShtProgbits = 1
	elfShtSymtab   = 2
	elfShtStrtab   = 3
	elfShtNote     = 7
	elfShtNobits   = 8

	elfShfAlloc = 0x2

	elfShentsize = 64 // Elf64_Shdr size
)

// elfSectionView is a mutable copy of one Elf64_Shdr plus its resolved name.
type elfSectionView struct {
	hdr  []byte // 64-byte header copy; edits here land in the rewritten table
	name string
}

func (s *elfSectionView) typ() uint64       { return uint64(binary.LittleEndian.Uint32(s.hdr[4:8])) }
func (s *elfSectionView) flags() uint64     { return binary.LittleEndian.Uint64(s.hdr[8:16]) }
func (s *elfSectionView) off() uint64       { return binary.LittleEndian.Uint64(s.hdr[24:32]) }
func (s *elfSectionView) size() uint64      { return binary.LittleEndian.Uint64(s.hdr[32:40]) }
func (s *elfSectionView) link() uint32      { return binary.LittleEndian.Uint32(s.hdr[40:44]) }
func (s *elfSectionView) addralign() uint64 { return binary.LittleEndian.Uint64(s.hdr[48:56]) }

func (s *elfSectionView) setType(v uint32) { binary.LittleEndian.PutUint32(s.hdr[4:8], v) }
func (s *elfSectionView) setOff(v uint64)  { binary.LittleEndian.PutUint64(s.hdr[24:32], v) }
func (s *elfSectionView) setLink(v uint32) { binary.LittleEndian.PutUint32(s.hdr[40:44], v) }

// hasContents reports whether the section occupies file bytes.
func (s *elfSectionView) hasContents() bool {
	return s.typ() != elfShtNull && s.typ() != elfShtNobits
}

// dropForDebug reports whether the slim/compact transforms drop this
// section's file contents: it is allocated (its bytes live in the APE's
// loadable span already) and it is not a note (notes are tiny, carry the
// build IDs tools look up, and sit in the preserved header page anyway).
func (s *elfSectionView) dropForDebug() bool {
	return s.flags()&elfShfAlloc != 0 && s.hasContents() && s.typ() != elfShtNote
}

// parseELFSections reads the section header table of an ELF image and
// resolves section names. The image must already have passed payloadFromELF
// validation for its ELF and program headers; the section table is
// validated here. Returns an error for images without a section table.
func parseELFSections(elf []byte) ([]*elfSectionView, error) {
	shoff := binary.LittleEndian.Uint64(elf[40:48])
	shentsize := binary.LittleEndian.Uint16(elf[58:60])
	shnum := binary.LittleEndian.Uint16(elf[60:62])
	shstrndx := binary.LittleEndian.Uint16(elf[62:64])
	if shoff == 0 || shnum == 0 {
		return nil, fmt.Errorf("no section header table")
	}
	if shentsize != elfShentsize {
		return nil, fmt.Errorf("e_shentsize is %d, want %d", shentsize, elfShentsize)
	}
	if shoff > uint64(len(elf)) || uint64(shnum)*elfShentsize > uint64(len(elf))-shoff {
		return nil, fmt.Errorf("section header table (e_shoff %#x, e_shnum %d) extends past end of image (%d bytes)", shoff, shnum, len(elf))
	}
	if shstrndx >= shnum {
		return nil, fmt.Errorf("e_shstrndx %d out of range (%d sections)", shstrndx, shnum)
	}
	secs := make([]*elfSectionView, shnum)
	for i := range secs {
		hdr := make([]byte, elfShentsize)
		copy(hdr, elf[shoff+uint64(i)*elfShentsize:])
		secs[i] = &elfSectionView{hdr: hdr}
	}
	strs := secs[shstrndx]
	strOff, strSize := strs.off(), strs.size()
	if strOff > uint64(len(elf)) || strSize > uint64(len(elf))-strOff {
		return nil, fmt.Errorf(".shstrtab (offset %#x, size %#x) extends past end of image", strOff, strSize)
	}
	strtab := elf[strOff : strOff+strSize]
	for i, s := range secs {
		nameOff := binary.LittleEndian.Uint32(s.hdr[0:4])
		if uint64(nameOff) >= strSize {
			return nil, fmt.Errorf("section %d name offset %#x out of range", i, nameOff)
		}
		name := strtab[nameOff:]
		for j, b := range name {
			if b == 0 {
				name = name[:j]
				break
			}
		}
		s.name = string(name)
	}
	for i, s := range secs {
		if !s.hasContents() {
			continue
		}
		if s.off() > uint64(len(elf)) || s.size() > uint64(len(elf))-s.off() {
			return nil, fmt.Errorf("section %d (%s) contents (offset %#x, size %#x) extend past end of image", i, s.name, s.off(), s.size())
		}
	}
	return secs, nil
}

// clampPhdrsToPrefix rewrites the program header table inside out (an image
// whose file contents end at prefixEnd) so that no header references file
// bytes past prefixEnd: fully-dropped spans become offset 0 / filesz 0,
// partially-retained spans are clamped. Virtual addresses and memory sizes
// are untouched, the way objcopy --only-keep-debug leaves the segment map
// describing the original memory image.
func clampPhdrsToPrefix(out []byte, prefixEnd uint64) {
	phoff := binary.LittleEndian.Uint64(out[32:40])
	phentsize := binary.LittleEndian.Uint16(out[54:56])
	phnum := binary.LittleEndian.Uint16(out[56:58])
	for i := uint16(0); i < phnum; i++ {
		ph := out[phoff+uint64(i)*uint64(phentsize):]
		off := binary.LittleEndian.Uint64(ph[8:16])
		filesz := binary.LittleEndian.Uint64(ph[32:40])
		switch {
		case off >= prefixEnd:
			binary.LittleEndian.PutUint64(ph[8:16], 0)
			binary.LittleEndian.PutUint64(ph[32:40], 0)
		case off+filesz > prefixEnd:
			binary.LittleEndian.PutUint64(ph[32:40], prefixEnd-off)
		}
	}
}

// alignTo pads b with zeros to the given alignment (a power of two, or 0/1
// for none) and returns the padded slice.
func alignTo(b []byte, align uint64) []byte {
	if align > 1 {
		for uint64(len(b))&(align-1) != 0 {
			b = append(b, 0)
		}
	}
	return b
}

// slimELFDebug builds the debug-only ("slim") form of a pristine per-arch
// linker ELF image, equivalent to objcopy --only-keep-debug:
//
//   - The header page - everything up to the first dropped section's file
//     offset (ELF header, program headers, and the note sections carrying
//     the build IDs) - is preserved verbatim at its original offsets.
//   - Allocated sections lose their file contents and become SHT_NOBITS;
//     their headers keep name, address, size, and flags, so debuggers can
//     still lay out the memory image they describe.
//   - Non-allocated sections (.debug_*, .symtab, .strtab, .shstrtab) keep
//     their contents, repacked after the header page.
//   - Program headers are clamped to the retained file span; virtual
//     addresses and memory sizes stay intact.
//
// The result is a valid, non-runnable ELF that every DWARF consumer reads
// like the pristine original (verified with gdb, delve, and llvm tools),
// at roughly a third of the size.
func slimELFDebug(elf []byte) ([]byte, error) {
	secs, err := parseELFSections(elf)
	if err != nil {
		return nil, err
	}

	// The preserved prefix ends where the first dropped section's
	// contents begin (in practice the end of the ELF header page: text
	// starts at the next page boundary, notes sit inside the first page).
	prefixEnd := uint64(len(elf))
	for _, s := range secs {
		if s.dropForDebug() && s.off() < prefixEnd {
			prefixEnd = s.off()
		}
	}
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	if hdrEnd := phoff + uint64(phnum)*56; prefixEnd < hdrEnd {
		return nil, fmt.Errorf("dropped section contents at %#x overlap the program header table ending at %#x", prefixEnd, hdrEnd)
	}

	out := append([]byte(nil), elf[:prefixEnd]...)
	clampPhdrsToPrefix(out, prefixEnd)

	for _, s := range secs {
		switch {
		case s.dropForDebug():
			s.setType(elfShtNobits)
			s.setOff(prefixEnd)
		case !s.hasContents():
			if s.typ() != elfShtNull {
				s.setOff(prefixEnd)
			}
		case s.off()+s.size() <= prefixEnd:
			// Entirely inside the preserved prefix (notes): bytes and
			// offset both unchanged.
		default:
			out = alignTo(out, s.addralign())
			newOff := uint64(len(out))
			out = append(out, elf[s.off():s.off()+s.size()]...)
			s.setOff(newOff)
		}
	}

	out = alignTo(out, 8)
	shoff := uint64(len(out))
	for _, s := range secs {
		out = append(out, s.hdr...)
	}
	binary.LittleEndian.PutUint64(out[40:48], shoff) // e_shoff
	// e_shnum/e_shstrndx are unchanged: the table keeps every section at
	// its original index, so symbol st_shndx values and sh_link fields in
	// the preserved headers stay valid.
	return out, nil
}

// apeCompactDropDebug lists the .debug_* sections the compact in-binary
// view leaves out: location lists, which serve variable/argument
// inspection (about a fifth of the compressed DWARF - inspecting
// variables is sidecar territory, and gdb degrades them cleanly to
// <optimized out>). Everything a file:line backtrace needs stays:
// .debug_info/.debug_abbrev for the DIE tree, .debug_line for line
// tables, .debug_rnglists/.debug_addr for DWARF v5 PC->CU mapping, and
// .debug_frame for CFI - measured at only ~50 KB zlib'd per arch, and
// without it gdb's fallback unwinder emits a bogus frame when stopped in
// a function prologue (verified: breakpoint at main.fizzbuzz+0 grew a
// "?? ()" frame between it and main.main). The slim sidecars keep
// everything, so full-fidelity debugging remains one file away.
var apeCompactDropDebug = map[string]bool{
	".debug_loclists": true,
}

// apeCompactView describes one payload's section-header view inside the
// compact debug tail: the values its ELF header must carry so that the
// assimilated binary exposes the view to debuggers.
type apeCompactView struct {
	shoff    uint64 // absolute APE file offset of the section header table
	shnum    uint16
	shstrndx uint16
}

// appendCompactDebugView appends one payload's compact debug view to tail
// (whose first byte will land at absolute APE file offset tailFileOff) and
// returns the grown tail plus the header fields describing the view.
//
// The view is a complete section table for the assimilated binary:
//
//   - Allocated sections keep their types and contents by POINTING INTO
//     THE PAYLOAD (sh_offset rebased by payloadOff): the APE already
//     ships those bytes in its loadable span, so they cost nothing.
//   - .symtab, .strtab, .shstrtab, and the kept .debug_* sections have
//     their contents packed into the tail at absolute offsets.
//   - Sections in apeCompactDropDebug are removed and the table is
//     renumbered (sh_link and e_shstrndx remapped, symbol st_shndx
//     values rewritten - in practice unchanged, since symbols reference
//     only allocated sections, which precede every dropped section).
//
// pristine must be the payload's ELF image as its linker produced it
// (payload-relative offsets, section table intact), payloadOff the file
// offset writeAPEFile will place it at.
func appendCompactDebugView(tail []byte, tailFileOff uint64, pristine []byte, payloadOff uint64) ([]byte, apeCompactView, error) {
	secs, err := parseELFSections(pristine)
	if err != nil {
		return nil, apeCompactView{}, err
	}

	// Decide what survives and assign new indices.
	newIdx := make(map[int]int, len(secs))
	kept := make([]*elfSectionView, 0, len(secs))
	for i, s := range secs {
		if s.flags()&elfShfAlloc == 0 && apeCompactDropDebug[s.name] {
			continue
		}
		newIdx[i] = len(kept)
		kept = append(kept, s)
	}

	remap := func(old uint32, what string) (uint32, error) {
		if old == 0 {
			return 0, nil
		}
		if int(old) >= len(secs) {
			return 0, fmt.Errorf("%s references section %d, beyond the %d-entry table", what, old, len(secs))
		}
		n, ok := newIdx[int(old)]
		if !ok {
			return 0, fmt.Errorf("%s references dropped section %d (%s)", what, old, secs[old].name)
		}
		return uint32(n), nil
	}

	for i, s := range secs {
		if _, ok := newIdx[i]; !ok {
			continue
		}
		if l, err := remap(s.link(), s.name+" sh_link"); err != nil {
			return nil, apeCompactView{}, err
		} else {
			s.setLink(l)
		}
		switch {
		case s.typ() == elfShtNull:
		case s.flags()&elfShfAlloc != 0:
			// Contents (if any) live inside the embedded payload.
			s.setOff(payloadOff + s.off())
		default:
			content := pristine[s.off() : s.off()+s.size()]
			if s.typ() == elfShtSymtab {
				remapped, err := remapSymtabShndx(content, newIdx, secs)
				if err != nil {
					return nil, apeCompactView{}, err
				}
				content = remapped
			}
			tail = alignTo(tail, s.addralign())
			s.setOff(tailFileOff + uint64(len(tail)))
			tail = append(tail, content...)
		}
	}

	tail = alignTo(tail, 8)
	view := apeCompactView{
		shoff: tailFileOff + uint64(len(tail)),
		shnum: uint16(len(kept)),
	}
	for _, s := range kept {
		tail = append(tail, s.hdr...)
	}
	shstrndx := int(binary.LittleEndian.Uint16(pristine[62:64]))
	n, ok := newIdx[shstrndx]
	if !ok {
		return nil, apeCompactView{}, fmt.Errorf(".shstrtab (section %d) was dropped", shstrndx)
	}
	view.shstrndx = uint16(n)
	return tail, view, nil
}

// remapSymtabShndx returns a copy of an Elf64 .symtab's contents with each
// symbol's st_shndx rewritten through newIdx. Symbols referencing dropped
// sections are an error (Go symbols reference only allocated sections,
// which the compact view always keeps).
func remapSymtabShndx(symtab []byte, newIdx map[int]int, secs []*elfSectionView) ([]byte, error) {
	const symSize = 24 // Elf64_Sym
	if len(symtab)%symSize != 0 {
		return nil, fmt.Errorf(".symtab size %d is not a multiple of %d", len(symtab), symSize)
	}
	out := append([]byte(nil), symtab...)
	for off := 0; off < len(out); off += symSize {
		shndx := binary.LittleEndian.Uint16(out[off+6 : off+8])
		if shndx == 0 || shndx >= 0xff00 { // SHN_UNDEF or reserved (SHN_ABS etc.)
			continue
		}
		if int(shndx) >= len(secs) {
			return nil, fmt.Errorf("symbol at %#x references section %d, beyond the %d-entry table", off, shndx, len(secs))
		}
		n, ok := newIdx[int(shndx)]
		if !ok {
			return nil, fmt.Errorf("symbol at %#x references dropped section %d (%s)", off, shndx, secs[shndx].name)
		}
		binary.LittleEndian.PutUint16(out[off+6:off+8], uint16(n))
	}
	return out, nil
}
