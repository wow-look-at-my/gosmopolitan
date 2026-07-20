// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"cmd/internal/sys"
)

// addTestDebugTail appends a non-loadable debug tail to elf, the way the
// linker's real output carries .symtab/.strtab and DWARF past the loadable
// span: a two-entry symbol table (null + a global "main.main"), a string
// table that also holds sentinel (so tests can detect the tail's presence
// in merged output), a section name table, and a section header table that
// the ELF header is patched to reference. The result parses with debug/elf
// and reports the symbol.
func addTestDebugTail(t *testing.T, elfImg []byte, sentinel string) []byte {
	t.Helper()

	out := append([]byte(nil), elfImg...)

	// Symbol table: null symbol + one global.
	symtab := make([]byte, 48)
	binary.LittleEndian.PutUint32(symtab[24:], 1)                   // st_name: offset in .strtab
	symtab[28] = 0x12                                               // st_info: GLOBAL | FUNC
	binary.LittleEndian.PutUint16(symtab[30:], uint16(elf.SHN_ABS)) // st_shndx
	binary.LittleEndian.PutUint64(symtab[32:], testELFEntry)
	binary.LittleEndian.PutUint64(symtab[40:], 8) // st_size

	strtab := []byte("\x00main.main\x00" + sentinel + "\x00")
	shstrtab := []byte("\x00.symtab\x00.strtab\x00.shstrtab\x00")

	symtabOff := uint64(len(out))
	out = append(out, symtab...)
	strtabOff := uint64(len(out))
	out = append(out, strtab...)
	shstrtabOff := uint64(len(out))
	out = append(out, shstrtab...)
	shoff := uint64(len(out))

	type shdr struct {
		name, typ, link, info uint32
		off, size, entsize    uint64
	}
	shdrs := []shdr{
		{}, // SHN_UNDEF
		{name: 1, typ: uint32(elf.SHT_SYMTAB), off: symtabOff, size: 48, link: 2, info: 1, entsize: 24},
		{name: 9, typ: uint32(elf.SHT_STRTAB), off: strtabOff, size: uint64(len(strtab))},
		{name: 17, typ: uint32(elf.SHT_STRTAB), off: shstrtabOff, size: uint64(len(shstrtab))},
	}
	for _, sh := range shdrs {
		b := make([]byte, 64)
		binary.LittleEndian.PutUint32(b[0:], sh.name)
		binary.LittleEndian.PutUint32(b[4:], sh.typ)
		binary.LittleEndian.PutUint64(b[24:], sh.off)
		binary.LittleEndian.PutUint64(b[32:], sh.size)
		binary.LittleEndian.PutUint32(b[40:], sh.link)
		binary.LittleEndian.PutUint32(b[44:], sh.info)
		binary.LittleEndian.PutUint64(b[48:], 1) // sh_addralign
		binary.LittleEndian.PutUint64(b[56:], sh.entsize)
		out = append(out, b...)
	}

	binary.LittleEndian.PutUint64(out[40:48], shoff)              // e_shoff
	binary.LittleEndian.PutUint16(out[58:60], 64)                 // e_shentsize
	binary.LittleEndian.PutUint16(out[60:62], uint16(len(shdrs))) // e_shnum
	binary.LittleEndian.PutUint16(out[62:64], 3)                  // e_shstrndx
	return out
}

const (
	testSentinelAMD64 = "SENTINEL_DEBUG_TAIL_AMD64"
	testSentinelARM64 = "SENTINEL_DEBUG_TAIL_ARM64"
)

// buildTestELFPair returns synthetic amd64 and arm64 linker outputs, each
// with full program headers plus a parseable symtab/debug tail carrying an
// arch-specific sentinel string.
func buildTestELFPair(t *testing.T) (amdElf, armElf []byte) {
	t.Helper()
	amdElf = addTestDebugTail(t, buildTestELF(t, testELFEntry, testELFPhdrs()), testSentinelAMD64)
	armElf = addTestDebugTail(t, buildTestELFForMachine(t, elfMachineARM64, testELFEntry, testELFPhdrs()), testSentinelARM64)
	return amdElf, armElf
}

// setAPEFatFlags sets the -apestrip/-apedbg flag values for one test.
func setAPEFatFlags(t *testing.T, strip, dbg bool) {
	t.Helper()
	oldStrip, oldDbg := *flagApeStrip, *flagApeDbg
	*flagApeStrip, *flagApeDbg = strip, dbg
	t.Cleanup(func() { *flagApeStrip, *flagApeDbg = oldStrip, oldDbg })
}

// mergeTestPair builds the synthetic ELF pair, stages the amd64 input as a
// thin APE and the arm64 input as a raw ELF (covering both accepted input
// forms), and runs apeFatMerge with the given -apestrip/-apedbg values. It
// returns the pristine input images and the merged output path.
func mergeTestPair(t *testing.T, strip, dbg bool) (amdElf, armElf []byte, out string) {
	t.Helper()
	amdElf, armElf = buildTestELFPair(t)
	dir := t.TempDir()

	amdIn := filepath.Join(dir, "amd.com")
	p, err := payloadFromELF(append([]byte(nil), amdElf...))
	if err != nil {
		t.Fatal(err)
	}
	writeAPEFile(amdIn, []*apePayload{p})
	armIn := filepath.Join(dir, "arm.elf")
	if err := os.WriteFile(armIn, armElf, 0644); err != nil {
		t.Fatal(err)
	}

	setAPEFatFlags(t, strip, dbg)
	out = filepath.Join(dir, "fat.com")
	apeFatMerge(amdIn+","+armIn, out)
	return amdElf, armElf, out
}

// checkSidecarELF parses a debug sidecar and verifies it is a standalone
// ELF for the expected machine whose symbol table is intact.
func checkSidecarELF(t *testing.T, path string, machine elf.Machine) {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("sidecar %s does not parse as ELF: %v", path, err)
	}
	defer f.Close()
	if f.Machine != machine {
		t.Errorf("sidecar %s machine = %v, want %v", path, f.Machine, machine)
	}
	syms, err := f.Symbols()
	if err != nil {
		t.Fatalf("sidecar %s has no readable symbol table: %v", path, err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "main.main" {
			found = true
		}
	}
	if !found {
		t.Errorf("sidecar %s symbol table lacks main.main (%d symbols)", path, len(syms))
	}
}

func TestAPEDebugSidecarName(t *testing.T) {
	if got := apeDebugSidecarName("app.com", sys.AMD64); got != "app.com.dbg" {
		t.Errorf("amd64 sidecar name = %q, want app.com.dbg", got)
	}
	if got := apeDebugSidecarName("app.com", sys.ARM64); got != "app.com.aarch64.elf" {
		t.Errorf("arm64 sidecar name = %q, want app.com.aarch64.elf", got)
	}
}

// TestAPEFatMergeStripAndSidecars merges a thin APE (amd64) with a raw ELF
// (arm64) under -apestrip -apedbg and verifies: the sidecars are pristine
// byte copies of the original linker ELFs with parseable symbol tables, and
// the fat APE embeds only each payload's loadable span with the section
// header fields zeroed - no symtab or debug bytes survive in the output.
func TestAPEFatMergeStripAndSidecars(t *testing.T) {
	amdElf, armElf, out := mergeTestPair(t, true, true)
	extent := payloadExtent(amdElf)
	if want := uint64(0x4800); extent != want {
		t.Fatalf("payloadExtent = %#x, want %#x (end of last PT_LOAD)", extent, want)
	}
	if extent >= uint64(len(amdElf)) {
		t.Fatalf("debug tail not past the loadable span: extent %#x, image %#x", extent, len(amdElf))
	}

	// Sidecars: pristine byte copies of the original linker outputs.
	amdSidecar, err := os.ReadFile(out + ".dbg")
	if err != nil {
		t.Fatalf("amd64 sidecar: %v", err)
	}
	if !bytes.Equal(amdSidecar, amdElf) {
		t.Errorf("amd64 sidecar is not byte-identical to the original ELF (thin-APE extraction must round-trip)")
	}
	armSidecar, err := os.ReadFile(out + ".aarch64.elf")
	if err != nil {
		t.Fatalf("arm64 sidecar: %v", err)
	}
	if !bytes.Equal(armSidecar, armElf) {
		t.Errorf("arm64 sidecar is not byte-identical to the original ELF")
	}
	checkSidecarELF(t, out+".dbg", elf.EM_X86_64)
	checkSidecarELF(t, out+".aarch64.elf", elf.EM_AARCH64)

	fat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	// Expected embedded payloads: loadable span only, section header
	// fields zeroed, p_offsets shifted to their APE file offsets.
	stripExpected := func(src []byte, offset uint64) []byte {
		e := append([]byte(nil), src[:payloadExtent(src)]...)
		binary.LittleEndian.PutUint64(e[40:48], 0)
		binary.LittleEndian.PutUint16(e[60:62], 0)
		binary.LittleEndian.PutUint16(e[62:64], 0)
		return shiftPOffsets(e, offset)
	}
	amdOff := uint64(apeHeaderSize)
	armOff := (amdOff + extent + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)

	wantAmd := stripExpected(amdElf, amdOff)
	if got := fat[amdOff : amdOff+uint64(len(wantAmd))]; !bytes.Equal(got, wantAmd) {
		t.Errorf("embedded amd64 payload is not the stripped loadable span")
	}
	wantArm := stripExpected(armElf, armOff)
	if uint64(len(fat)) != armOff+uint64(len(wantArm)) {
		t.Fatalf("fat APE is %#x bytes, want %#x (arm64 payload at %#x must end the file)", len(fat), armOff+uint64(len(wantArm)), armOff)
	}
	if got := fat[armOff:]; !bytes.Equal(got, wantArm) {
		t.Errorf("embedded arm64 payload is not the stripped loadable span")
	}

	// No debug bytes anywhere in the shipped APE.
	for _, sentinel := range []string{testSentinelAMD64, testSentinelARM64} {
		if bytes.Contains(fat, []byte(sentinel)) {
			t.Errorf("fat APE still contains debug tail sentinel %q", sentinel)
		}
	}

	// Re-merge rejection must still detect the second payload in a
	// stripped fat APE.
	if !hasSecondAPEPayload(fat) {
		t.Errorf("hasSecondAPEPayload = false for stripped fat APE; re-merge rejection is broken")
	}
}

// TestAPEFatMergeDefaultUnchanged verifies that without -apestrip/-apedbg
// the merge embeds the full payloads byte-for-byte (only p_offsets shifted)
// and writes no sidecar files - today's behavior, which GOCOSMOSTRIP=0 and
// user -ldflags -s/-w builds rely on.
func TestAPEFatMergeDefaultUnchanged(t *testing.T) {
	amdElf, armElf, out := mergeTestPair(t, false, false)

	fat, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	amdOff := uint64(apeHeaderSize)
	armOff := (amdOff + uint64(len(amdElf)) + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)
	wantAmd := shiftPOffsets(amdElf, amdOff)
	if got := fat[amdOff : amdOff+uint64(len(wantAmd))]; !bytes.Equal(got, wantAmd) {
		t.Errorf("embedded amd64 payload differs from the full input image")
	}
	wantArm := shiftPOffsets(armElf, armOff)
	if uint64(len(fat)) != armOff+uint64(len(wantArm)) {
		t.Fatalf("fat APE is %#x bytes, want %#x", len(fat), armOff+uint64(len(wantArm)))
	}
	if got := fat[armOff:]; !bytes.Equal(got, wantArm) {
		t.Errorf("embedded arm64 payload differs from the full input image")
	}
	for _, sentinel := range []string{testSentinelAMD64, testSentinelARM64} {
		if !bytes.Contains(fat, []byte(sentinel)) {
			t.Errorf("full merge lost debug tail sentinel %q", sentinel)
		}
	}
	for _, sidecar := range []string{out + ".dbg", out + ".aarch64.elf"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("sidecar %s written without -apedbg", sidecar)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat %s: %v", sidecar, err)
		}
	}
}
