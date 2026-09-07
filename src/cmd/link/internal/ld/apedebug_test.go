// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"testing"

	"cmd/internal/sys"
)

// Elf64 constants for the synthetic section tables built below.
const (
	testShfWrite = 0x1
	testShfAlloc = 0x2
	testShfExec  = 0x4
)

// testShdr describes one section for addTestSectionedTail.
type testShdr struct {
	nameOff            uint32
	typ                uint32
	flags, addr        uint64
	off, size          uint64
	link, info         uint32
	addralign, entsize uint64
}

func (sh *testShdr) encode() []byte {
	b := make([]byte, 64)
	binary.LittleEndian.PutUint32(b[0:], sh.nameOff)
	binary.LittleEndian.PutUint32(b[4:], sh.typ)
	binary.LittleEndian.PutUint64(b[8:], sh.flags)
	binary.LittleEndian.PutUint64(b[16:], sh.addr)
	binary.LittleEndian.PutUint64(b[24:], sh.off)
	binary.LittleEndian.PutUint64(b[32:], sh.size)
	binary.LittleEndian.PutUint32(b[40:], sh.link)
	binary.LittleEndian.PutUint32(b[44:], sh.info)
	binary.LittleEndian.PutUint64(b[48:], sh.addralign)
	binary.LittleEndian.PutUint64(b[56:], sh.entsize)
	return b
}

// addTestSectionedTail appends a full-fidelity section table to elf (a
// buildTestELF image using testELFPhdrs), shaped like the cosmo linker's
// real output: allocated sections covering the loadable spans (a note
// inside the header page, text/rodata/data, a BSS), non-allocated debug
// sections carrying sentinel content, and a symbol table whose main.main
// references .text. The result parses with debug/elf.
func addTestSectionedTail(t *testing.T, elfImg []byte, sentinel string) []byte {
	t.Helper()

	out := append([]byte(nil), elfImg...)

	debugInfo := []byte("DWARFINFO(" + sentinel + ")")
	debugLoclists := []byte("LOCLISTS(" + sentinel + ")")

	// Symbol table: null symbol + one global main.main in .text (index 2).
	symtab := make([]byte, 48)
	binary.LittleEndian.PutUint32(symtab[24:], 1) // st_name
	symtab[28] = 0x12                             // st_info: GLOBAL | FUNC
	binary.LittleEndian.PutUint16(symtab[30:], 2) // st_shndx: .text
	binary.LittleEndian.PutUint64(symtab[32:], testELFEntry)
	binary.LittleEndian.PutUint64(symtab[40:], 8) // st_size

	strtab := []byte("\x00main.main\x00")
	shstrtab := []byte("\x00.note.test\x00.text\x00.rodata\x00.data\x00.bss\x00.debug_info\x00.debug_loclists\x00.symtab\x00.strtab\x00.shstrtab\x00")

	appendPart := func(b []byte, align uint64) uint64 {
		out = alignTo(out, align)
		off := uint64(len(out))
		out = append(out, b...)
		return off
	}
	infoOff := appendPart(debugInfo, 1)
	loclistsOff := appendPart(debugLoclists, 1)
	symtabOff := appendPart(symtab, 8)
	strtabOff := appendPart(strtab, 1)
	shstrtabOff := appendPart(shstrtab, 1)

	shdrs := []testShdr{
		{},
		{nameOff: 1, typ: elfShtNote, flags: testShfAlloc, addr: 0x100000f00, off: 0xf00, size: 0x64, addralign: 4},
		{nameOff: 12, typ: elfShtProgbits, flags: testShfAlloc | testShfExec, addr: 0x100001000, off: 0x1000, size: 0x1345, addralign: 16},
		{nameOff: 18, typ: elfShtProgbits, flags: testShfAlloc, addr: 0x100003000, off: 0x3000, size: 0x1000, addralign: 32},
		{nameOff: 26, typ: elfShtProgbits, flags: testShfAlloc | testShfWrite, addr: 0x100004000, off: 0x4000, size: 0x800, addralign: 32},
		{nameOff: 32, typ: elfShtNobits, flags: testShfAlloc | testShfWrite, addr: 0x100004800, off: 0x4800, size: 0x2000, addralign: 32},
		{nameOff: 37, typ: elfShtProgbits, off: infoOff, size: uint64(len(debugInfo)), addralign: 1},
		{nameOff: 49, typ: elfShtProgbits, off: loclistsOff, size: uint64(len(debugLoclists)), addralign: 1},
		{nameOff: 65, typ: elfShtSymtab, off: symtabOff, size: 48, link: 9, info: 1, addralign: 8, entsize: 24},
		{nameOff: 73, typ: elfShtStrtab, off: strtabOff, size: uint64(len(strtab)), addralign: 1},
		{nameOff: 81, typ: elfShtStrtab, off: shstrtabOff, size: uint64(len(shstrtab)), addralign: 1},
	}
	out = alignTo(out, 8)
	shoff := uint64(len(out))
	for i := range shdrs {
		out = append(out, shdrs[i].encode()...)
	}
	binary.LittleEndian.PutUint64(out[40:48], shoff)              // e_shoff
	binary.LittleEndian.PutUint16(out[58:60], 64)                 // e_shentsize
	binary.LittleEndian.PutUint16(out[60:62], uint16(len(shdrs))) // e_shnum
	binary.LittleEndian.PutUint16(out[62:64], 10)                 // e_shstrndx
	return out
}

// testTextMarker is planted inside the synthetic .text span (at the entry
// point's file offset) so tests can verify section views reference the
// real payload bytes.
const testTextMarker = "TEXTMARKER"

// buildTestSectionedELFPair returns synthetic amd64 and arm64 linker
// outputs with full-fidelity section tables (see addTestSectionedTail).
func buildTestSectionedELFPair(t *testing.T) (amdElf, armElf []byte) {
	t.Helper()
	amd := buildTestELF(t, testELFEntry, testELFPhdrs())
	copy(amd[0x1200:], testTextMarker)
	arm := buildTestELFForMachine(t, elfMachineARM64, testELFEntry, testELFPhdrs())
	copy(arm[0x1200:], testTextMarker)
	amdElf = addTestSectionedTail(t, amd, testSentinelAMD64)
	armElf = addTestSectionedTail(t, arm, testSentinelARM64)
	return amdElf, armElf
}

// checkSlimELF verifies the invariants of a slim (only-keep-debug
// equivalent) image derived from an addTestSectionedTail input: allocated
// contents dropped, headers and addresses preserved, debug contents and
// symbol table intact, program headers clamped to the retained span.
func checkSlimELF(t *testing.T, slim, orig []byte, machine elf.Machine, sentinel string) {
	t.Helper()

	if len(slim) >= len(orig) {
		t.Errorf("slim image is %d bytes, not smaller than the %d-byte original", len(slim), len(orig))
	}

	f, err := elf.NewFile(bytes.NewReader(slim))
	if err != nil {
		t.Fatalf("slim image does not parse as ELF: %v", err)
	}
	defer f.Close()
	if f.Machine != machine {
		t.Errorf("machine = %v, want %v", f.Machine, machine)
	}
	if f.Type != elf.ET_EXEC {
		t.Errorf("type = %v, want ET_EXEC", f.Type)
	}

	// Allocated contents dropped, addresses and sizes preserved.
	for _, want := range []struct {
		name  string
		typ   elf.SectionType
		addr  uint64
		size  uint64
		flags elf.SectionFlag
	}{
		{".text", elf.SHT_NOBITS, 0x100001000, 0x1345, elf.SHF_ALLOC | elf.SHF_EXECINSTR},
		{".rodata", elf.SHT_NOBITS, 0x100003000, 0x1000, elf.SHF_ALLOC},
		{".data", elf.SHT_NOBITS, 0x100004000, 0x800, elf.SHF_ALLOC | elf.SHF_WRITE},
		{".bss", elf.SHT_NOBITS, 0x100004800, 0x2000, elf.SHF_ALLOC | elf.SHF_WRITE},
	} {
		s := f.Section(want.name)
		if s == nil {
			t.Errorf("section %s missing", want.name)
			continue
		}
		if s.Type != want.typ || s.Addr != want.addr || s.Size != want.size || s.Flags != want.flags {
			t.Errorf("section %s = type %v addr %#x size %#x flags %v, want type %v addr %#x size %#x flags %v",
				want.name, s.Type, s.Addr, s.Size, s.Flags, want.typ, want.addr, want.size, want.flags)
		}
	}

	// Note contents preserved verbatim (they live in the retained header
	// page at their original offset).
	note := f.Section(".note.test")
	if note == nil {
		t.Fatalf("note section missing")
	}
	if note.Offset != 0xf00 || note.Type != elf.SHT_NOTE {
		t.Errorf("note = type %v offset %#x, want SHT_NOTE at 0xf00", note.Type, note.Offset)
	}
	noteData, err := note.Data()
	if err != nil {
		t.Fatalf("note contents unreadable: %v", err)
	}
	if !bytes.Equal(noteData, orig[0xf00:0xf00+0x64]) {
		t.Errorf("note contents differ from the original image")
	}

	// Debug contents intact.
	for name, want := range map[string]string{
		".debug_info":     "DWARFINFO(" + sentinel + ")",
		".debug_loclists": "LOCLISTS(" + sentinel + ")",
	} {
		s := f.Section(name)
		if s == nil {
			t.Errorf("section %s missing", name)
			continue
		}
		data, err := s.Data()
		if err != nil {
			t.Errorf("section %s unreadable: %v", name, err)
			continue
		}
		if string(data) != want {
			t.Errorf("section %s = %q, want %q", name, data, want)
		}
	}

	// Symbol table intact and still referencing .text.
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("no readable symbol table: %v", err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "main.main" {
			found = true
			if s.Value != testELFEntry {
				t.Errorf("main.main value = %#x, want %#x", s.Value, testELFEntry)
			}
			if s.Section != elf.SectionIndex(2) {
				t.Errorf("main.main section index = %d, want 2 (.text)", s.Section)
			}
		}
	}
	if !found {
		t.Errorf("symbol table lacks main.main (%d symbols)", len(syms))
	}

	// Program headers clamped to the retained header page; memory image
	// untouched.
	if len(f.Progs) != 4 {
		t.Fatalf("got %d program headers, want 4", len(f.Progs))
	}
	if p := f.Progs[0]; p.Type != elf.PT_NOTE || p.Off != 0xf00 || p.Filesz != 0x64 {
		t.Errorf("NOTE phdr = %+v, want off 0xf00 filesz 0x64 (inside the retained page)", p.ProgHeader)
	}
	if p := f.Progs[1]; p.Off != 0 || p.Filesz != 0x1000 || p.Vaddr != 0x100000000 || p.Memsz != 0x2345 {
		t.Errorf("text LOAD = %+v, want filesz clamped to 0x1000 with vaddr/memsz intact", p.ProgHeader)
	}
	for i, p := range f.Progs[2:] {
		if p.Off != 0 || p.Filesz != 0 {
			t.Errorf("dropped LOAD %d = off %#x filesz %#x, want 0/0", i+2, p.Off, p.Filesz)
		}
		if p.Memsz == 0 || p.Vaddr == 0 {
			t.Errorf("dropped LOAD %d lost its memory image (%+v)", i+2, p.ProgHeader)
		}
	}
}

func TestAPESlimELFDebug(t *testing.T) {
	amdElf, armElf := buildTestSectionedELFPair(t)

	amdSlim, err := slimELFDebug(amdElf)
	if err != nil {
		t.Fatalf("slimELFDebug(amd64): %v", err)
	}
	checkSlimELF(t, amdSlim, amdElf, elf.EM_X86_64, testSentinelAMD64)

	armSlim, err := slimELFDebug(armElf)
	if err != nil {
		t.Fatalf("slimELFDebug(arm64): %v", err)
	}
	checkSlimELF(t, armSlim, armElf, elf.EM_AARCH64, testSentinelARM64)
}

// setAPEDbgMode sets the -apedbgmode flag value for one test.
func setAPEDbgMode(t *testing.T, mode string) {
	t.Helper()
	t.Serial("the debug-mode flag is a package global that every merge in this process reads")
	old := *flagApeDbgMode
	*flagApeDbgMode = mode
	t.Cleanup(func() { *flagApeDbgMode = old })
}

// mergeSectionedPair stages a sectioned synthetic pair (amd64 as a thin
// APE, arm64 as a raw ELF) and merges under -apestrip -apedbg with the
// given -apedbgmode. It returns the input images and the output path.
func mergeSectionedPair(t *testing.T, mode string) (amdElf, armElf []byte, out string) {
	t.Helper()
	amdElf, armElf = buildTestSectionedELFPair(t)
	dir := t.TempDir()

	amdIn := dir + "/amd.com"
	p, err := payloadFromELF(append([]byte(nil), amdElf...))
	if err != nil {
		t.Fatal(err)
	}
	writeAPEFile(amdIn, []*apePayload{p})
	armIn := dir + "/arm.elf"
	if err := os.WriteFile(armIn, armElf, 0644); err != nil {
		t.Fatal(err)
	}

	setAPEFatFlags(t, true, true)
	setAPEDbgMode(t, mode)
	out = dir + "/fat.com"
	apeFatMerge(amdIn+","+armIn, out)
	return amdElf, armElf, out
}

// TestAPEFatMergeSlimSidecars merges under -apedbgmode=slim and verifies
// the sidecars are debug-only images while the fat APE itself is
// byte-identical to a default (-apedbgmode=full) merge: the mode changes
// only what the sidecars carry.
func TestAPEFatMergeSlimSidecars(t *testing.T) {
	amdElf, armElf, out := mergeSectionedPair(t, "slim")

	amdSidecar, err := os.ReadFile(out + ".dbg")
	if err != nil {
		t.Fatalf("amd64 sidecar: %v", err)
	}
	checkSlimELF(t, amdSidecar, amdElf, elf.EM_X86_64, testSentinelAMD64)
	armSidecar, err := os.ReadFile(out + ".aarch64.elf")
	if err != nil {
		t.Fatalf("arm64 sidecar: %v", err)
	}
	checkSlimELF(t, armSidecar, armElf, elf.EM_AARCH64, testSentinelARM64)

	// The slim sidecar must match slimELFDebug of the pristine input
	// exactly (the merge pipeline adds nothing else).
	wantAmd, err := slimELFDebug(amdElf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(amdSidecar, wantAmd) {
		t.Errorf("amd64 sidecar differs from slimELFDebug of the input image")
	}

	// No debug bytes anywhere in the shipped APE (strip still applies).
	fat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{testSentinelAMD64, testSentinelARM64} {
		if bytes.Contains(fat, []byte(sentinel)) {
			t.Errorf("fat APE still contains debug sentinel %q", sentinel)
		}
	}

	// The fat APE is byte-identical to a full-mode merge of the same
	// inputs: slim affects sidecars only.
	_, _, outFull := mergeSectionedPair(t, "full")
	fatFull, err := os.ReadFile(outFull)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fat, fatFull) {
		t.Errorf("slim-mode fat APE differs from full-mode fat APE (mode must only affect sidecars)")
	}

	// And the full-mode sidecar is still the pristine byte copy.
	fullSidecar, err := os.ReadFile(outFull + ".dbg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fullSidecar, amdElf) {
		t.Errorf("full-mode sidecar is not byte-identical to the original ELF")
	}
}

// checkCompactView simulates self-assimilation of one architecture's
// payload (overlaying the boot ELF header the merge embedded for it) and
// verifies the resulting file exposes a debugger-consumable section view:
// allocated sections referencing the real payload bytes, kept debug
// sections and the symbol table in the appended tail, dropped sections
// absent, indices consistent.
func checkCompactView(t *testing.T, fat []byte, payloadOff, payloadLen uint64, arch sys.ArchFamily, machine elf.Machine, sentinel string) {
	t.Helper()

	boot := makeEmbeddedElfHeader(fat[payloadOff:payloadOff+payloadLen], payloadOff, arch)
	assim := append([]byte(nil), fat...)
	copy(assim[:64], boot)

	f, err := elf.NewFile(bytes.NewReader(assim))
	if err != nil {
		t.Fatalf("assimilated compact APE does not parse as ELF: %v", err)
	}
	defer f.Close()
	if f.Machine != machine {
		t.Errorf("machine = %v, want %v", f.Machine, machine)
	}

	// The stored payload's own ELF header must advertise the same view
	// (both copies matter: the stored one for tools reading the payload,
	// the boot one for the assimilated file).
	stored := fat[payloadOff:]
	if got := binary.LittleEndian.Uint64(boot[40:48]); got != binary.LittleEndian.Uint64(stored[40:48]) {
		t.Errorf("boot e_shoff %#x differs from stored payload e_shoff %#x", got, binary.LittleEndian.Uint64(stored[40:48]))
	}
	if shoff := binary.LittleEndian.Uint64(stored[40:48]); shoff < payloadOff+payloadLen {
		t.Errorf("e_shoff %#x lies inside the payload span ending at %#x", shoff, payloadOff+payloadLen)
	}

	// Dropped section gone, kept debug content intact.
	if f.Section(".debug_loclists") != nil {
		t.Errorf(".debug_loclists survived in the compact view")
	}
	info := f.Section(".debug_info")
	if info == nil {
		t.Fatalf(".debug_info missing from the compact view")
	}
	data, err := info.Data()
	if err != nil {
		t.Fatalf(".debug_info unreadable: %v", err)
	}
	if want := "DWARFINFO(" + sentinel + ")"; string(data) != want {
		t.Errorf(".debug_info = %q, want %q", data, want)
	}

	// Allocated sections keep PROGBITS and point into the embedded
	// payload's real bytes.
	text := f.Section(".text")
	if text == nil {
		t.Fatalf(".text missing from the compact view")
	}
	if text.Type != elf.SHT_PROGBITS || text.Addr != 0x100001000 || text.Offset != payloadOff+0x1000 || text.Size != 0x1345 {
		t.Errorf(".text = type %v addr %#x off %#x size %#x, want PROGBITS at addr 0x100001000 off %#x size 0x1345",
			text.Type, text.Addr, text.Offset, text.Size, payloadOff+0x1000)
	}
	textData, err := text.Data()
	if err != nil {
		t.Fatalf(".text unreadable: %v", err)
	}
	if got := string(textData[0x200 : 0x200+uint64(len(testTextMarker))]); got != testTextMarker {
		t.Errorf(".text does not reference the payload bytes: marker = %q, want %q", got, testTextMarker)
	}

	// Symbol table intact, strtab link remapped, st_shndx still .text.
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("no readable symbol table in the compact view: %v", err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "main.main" {
			found = true
			if s.Value != testELFEntry || s.Section != elf.SectionIndex(2) {
				t.Errorf("main.main = value %#x section %d, want value %#x section 2 (.text)", s.Value, s.Section, uint64(testELFEntry))
			}
		}
	}
	if !found {
		t.Errorf("symbol table lacks main.main (%d symbols)", len(syms))
	}
}

// TestAPEFatMergeCompact merges under -apedbgmode=compact and verifies:
// slim sidecars, a debug tail appended past the last payload, payload and
// boot ELF headers referencing per-arch section views (simulated
// assimilation parses for both architectures), and - outside the 12
// patched ELF-header bytes per payload - a fat image byte-identical to
// the default merge.
func TestAPEFatMergeCompact(t *testing.T) {
	amdElf, armElf, out := mergeSectionedPair(t, "compact")

	// Sidecars are slim in compact mode.
	amdSidecar, err := os.ReadFile(out + ".dbg")
	if err != nil {
		t.Fatalf("amd64 sidecar: %v", err)
	}
	checkSlimELF(t, amdSidecar, amdElf, elf.EM_X86_64, testSentinelAMD64)

	fat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	extent := payloadExtent(amdElf)
	amdOff := uint64(apeHeaderSize)
	armOff := (amdOff + extent + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)
	armEnd := armOff + payloadExtent(armElf)
	if uint64(len(fat)) <= armEnd {
		t.Fatalf("fat APE is %#x bytes, want a debug tail past the last payload end %#x", len(fat), armEnd)
	}

	checkCompactView(t, fat, amdOff, extent, sys.AMD64, elf.EM_X86_64, testSentinelAMD64)
	checkCompactView(t, fat, armOff, payloadExtent(armElf), sys.ARM64, elf.EM_AARCH64, testSentinelARM64)

	// Aside from each payload's patched e_shoff/e_shnum/e_shstrndx (and
	// the boot headers inside the shell script, which carry the same
	// fields), the image up to the tail is byte-identical to a default
	// merge.
	_, _, outFull := mergeSectionedPair(t, "full")
	fatFull, err := os.ReadFile(outFull)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(fatFull)) != armEnd {
		t.Fatalf("full-mode fat APE is %#x bytes, want %#x", len(fatFull), armEnd)
	}
	neutralized := append([]byte(nil), fat[:armEnd]...)
	for _, off := range []uint64{amdOff, armOff} {
		for i := off + 40; i < off+48; i++ {
			neutralized[i] = 0
		}
		for i := off + 60; i < off+64; i++ {
			neutralized[i] = 0
		}
	}
	if !bytes.Equal(neutralized[amdOff:], fatFull[amdOff:]) {
		t.Errorf("compact payload spans differ from the default merge beyond the patched ELF header fields")
	}
	// Head: everything outside the script window (PE header before it,
	// Mach-O header and APE loader after it) is unchanged; only the
	// printf-encoded boot headers inside the script differ.
	if !bytes.Equal(neutralized[:apeScriptOffset], fatFull[:apeScriptOffset]) {
		t.Errorf("compact APE head differs before the script region")
	}
	if !bytes.Equal(neutralized[apeMachoOffset:amdOff], fatFull[apeMachoOffset:amdOff]) {
		t.Errorf("compact APE head differs after the script region")
	}

	// Re-merge rejection still detects the second payload.
	if !hasSecondAPEPayload(fat) {
		t.Errorf("hasSecondAPEPayload = false for compact fat APE")
	}
}
