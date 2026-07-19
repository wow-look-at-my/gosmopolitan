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
