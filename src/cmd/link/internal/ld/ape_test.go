// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"testing"
)

// printfBlobTestInput returns a blob exercising every byte value plus
// adjacency cases where an octal escape is immediately followed by an
// octal digit, which must not be absorbed into the escape.
func printfBlobTestInput() []byte {
	blob := make([]byte, 0, 256+16)
	for i := 0; i < 256; i++ {
		blob = append(blob, byte(i))
	}
	// Escaped byte followed by octal digits: '%' -> \045, then literal "7".
	blob = append(blob, '%', '7', '\'', '0', '\\', '1', 0x00, '2', 0xff, '3')
	return blob
}

// decodePrintfBlob decodes the body of a printf '...' format string the way
// both POSIX printf and the APE loader's header scanner do: a backslash
// introduces an octal escape of one to three digits, and every other byte is
// taken literally.
func decodePrintfBlob(t *testing.T, s string) []byte {
	t.Helper()
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		if i+1 >= len(s) || s[i+1] < '0' || s[i+1] > '7' {
			t.Fatalf("backslash at offset %d is not followed by an octal digit", i)
		}
		v := 0
		n := 0
		for n < 3 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
			v = v*8 + int(s[i+1]-'0')
			i++
			n++
		}
		out = append(out, byte(v))
	}
	return out
}

func TestWritePrintfBlobEscaping(t *testing.T) {
	blob := printfBlobTestInput()

	var script bytes.Buffer
	writePrintfBlob(&script, blob)
	enc := script.String()

	for i := 0; i < len(enc); i++ {
		c := enc[i]
		if c < 0x20 || c >= 0x7f {
			t.Errorf("offset %d: encoded byte %#02x is not printable ASCII", i, c)
		}
		switch c {
		case '%':
			// printf would interpret a bare % as a conversion directive.
			t.Errorf("offset %d: bare %% in encoded blob", i)
		case '\'':
			// A raw quote terminates both the shell string and the APE
			// loader's scan of the printf statement.
			t.Errorf("offset %d: bare single quote in encoded blob", i)
		case '\\':
			// Backslashes may appear only as octal escape lead-ins.
			if i+1 >= len(enc) || enc[i+1] < '0' || enc[i+1] > '7' {
				t.Errorf("offset %d: backslash not followed by octal digit", i)
			}
		}
	}

	got := decodePrintfBlob(t, enc)
	if !bytes.Equal(got, blob) {
		t.Errorf("decoded blob does not round-trip:\ngot  %x\nwant %x", got, blob)
	}
}

// The loader directories are tried in order, and RAM comes first. A tmpfs
// directory keeps the bytes off every disk, which is the whole reason a host
// with no loader still writes nothing anybody can find. A caller that has
// neither directory replaces the list with APE_LOADERDIR.
func TestApeLoaderDirsPreferRAM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh on windows")
	}
	testenv.MustHaveExecPath(t, "sh")

	got := runAndCapture(t, "sh", "-c", `for d in `+apeLoaderDirs+`; do printf '%s\n' "$d"; done`)
	if want := "/dev/shm\n/tmp\n"; got != want {
		t.Errorf("default loader directories = %q, want %q", got, want)
	}

	got = runAndCapture(t, "sh", "-c", `APE_LOADERDIR=/somewhere; for d in `+apeLoaderDirs+`; do printf '%s\n' "$d"; done`)
	if want := "/somewhere\n"; got != want {
		t.Errorf("APE_LOADERDIR must replace the list, got %q, want %q", got, want)
	}
}

func runAndCapture(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// TestWritePrintfBlobShellRoundTrip runs the encoded blob through a real
// POSIX shell's printf, the way APE self-assimilation does, and checks that
// the bytes written match the input exactly.
func TestWritePrintfBlobShellRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh on windows")
	}
	testenv.MustHaveExecPath(t, "sh")

	blob := printfBlobTestInput()

	var script bytes.Buffer
	writePrintfBlob(&script, blob)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.bin")
	shPath := filepath.Join(dir, "blob.sh")
	// Mirror the APE bootstrap: open an fd on the target and printf into it.
	shellScript := "exec 7<> \"$1\" || exit 121\nprintf '" + script.String() + "' >&7\n"
	if err := os.WriteFile(shPath, []byte(shellScript), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sh", shPath, outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh printf failed: %v\n%s", err, out)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Errorf("shell printf output does not round-trip:\ngot  %x\nwant %x", got, blob)
	}
}

// testProgHeader describes one ELF program header for buildTestELF.
type testProgHeader struct {
	ptype  uint32
	flags  uint32
	off    uint64 // payload-relative file offset
	vaddr  uint64
	filesz uint64
	memsz  uint64
}

// Program header type and flag values (Elf64_Phdr).
const (
	elfPTLoad = 1
	elfPTNote = 4
	elfPFX    = 1
	elfPFW    = 2
	elfPFR    = 4
)

// buildTestELF assembles a minimal ELF64 amd64 executable image consisting
// of an ELF header, the given program headers, and zero-filled bodies large
// enough to cover every header's file range. Layout matches what the cosmo
// linker emits: e_phoff 64, e_phentsize 56.
func buildTestELF(t *testing.T, entry uint64, phdrs []testProgHeader) []byte {
	t.Helper()
	return buildTestELFForMachine(t, elfMachineAMD64, entry, phdrs)
}

// buildTestELFForMachine is buildTestELF with an explicit e_machine value.
func buildTestELFForMachine(t *testing.T, machine uint16, entry uint64, phdrs []testProgHeader) []byte {
	t.Helper()

	size := uint64(64 + 56*len(phdrs))
	for _, ph := range phdrs {
		if end := ph.off + ph.filesz; end > size {
			size = end
		}
	}
	elf := make([]byte, size)

	copy(elf[0:4], elfMagic)
	elf[4] = elfClass64
	elf[5] = elfDataLSB
	elf[6] = 1
	elf[7] = elfOSABIFreeBSD
	binary.LittleEndian.PutUint16(elf[16:], elfTypeExec)
	binary.LittleEndian.PutUint16(elf[18:], machine)
	binary.LittleEndian.PutUint32(elf[20:], 1)
	binary.LittleEndian.PutUint64(elf[24:], entry)
	binary.LittleEndian.PutUint64(elf[32:], 64) // e_phoff
	binary.LittleEndian.PutUint16(elf[52:], 64) // e_ehsize
	binary.LittleEndian.PutUint16(elf[54:], 56) // e_phentsize
	binary.LittleEndian.PutUint16(elf[56:], uint16(len(phdrs)))

	for i, ph := range phdrs {
		p := elf[64+56*i:]
		binary.LittleEndian.PutUint32(p[0:], ph.ptype)
		binary.LittleEndian.PutUint32(p[4:], ph.flags)
		binary.LittleEndian.PutUint64(p[8:], ph.off)
		binary.LittleEndian.PutUint64(p[16:], ph.vaddr)
		binary.LittleEndian.PutUint64(p[24:], ph.vaddr) // p_paddr
		binary.LittleEndian.PutUint64(p[32:], ph.filesz)
		binary.LittleEndian.PutUint64(p[40:], ph.memsz)
		binary.LittleEndian.PutUint64(p[48:], 0x1000) // p_align
	}
	return elf
}

// testELFPhdrs is a program header table shaped like the cosmo linker's
// amd64 output: an executable text load (which also covers the ELF header),
// a read-only load, and a writable load whose p_memsz exceeds p_filesz
// (BSS). A PT_NOTE is included to check that non-LOAD entries are skipped.
func testELFPhdrs() []testProgHeader {
	return []testProgHeader{
		{ptype: elfPTNote, flags: elfPFR, off: 0xf00, vaddr: 0x100000f00, filesz: 0x64, memsz: 0x64},
		{ptype: elfPTLoad, flags: elfPFR | elfPFX, off: 0, vaddr: 0x100000000, filesz: 0x2345, memsz: 0x2345},
		{ptype: elfPTLoad, flags: elfPFR, off: 0x3000, vaddr: 0x100003000, filesz: 0x1000, memsz: 0x1000},
		{ptype: elfPTLoad, flags: elfPFR | elfPFW, off: 0x4000, vaddr: 0x100004000, filesz: 0x800, memsz: 0x2800},
	}
}

const testELFEntry = 0x100001200

// buildTestNTELF returns a synthetic amd64 payload with the NT import
// blob (runtime.ntidata) and IAT (runtime.ntiat) placed in its RW load
// exactly as apePrepareNTBoot would leave them after patching, plus the
// matching apePEInfo. The blob sits at payload offset (== RVA) 0x4100,
// the IAT at 0x4180, both file-backed within the RW load's p_filesz.
func buildTestNTELF(t *testing.T) ([]byte, *apePEInfo) {
	t.Helper()
	elf := buildTestELF(t, testELFEntry, testELFPhdrs())

	const idataRVA, iatRVA = 0x4100, 0x4180
	blob := elf[idataRVA : idataRVA+ntidataSize]
	binary.LittleEndian.PutUint32(blob[0x00:], idataRVA+ntidataILT)     // IDT[0].OriginalFirstThunk
	binary.LittleEndian.PutUint32(blob[0x0C:], idataRVA+ntidataDLLName) // IDT[0].Name
	binary.LittleEndian.PutUint32(blob[0x10:], iatRVA)                  // IDT[0].FirstThunk
	binary.LittleEndian.PutUint64(blob[ntidataILT:], idataRVA+ntidataHintGetProc)
	binary.LittleEndian.PutUint64(blob[ntidataILT+8:], idataRVA+ntidataHintLoadLib)
	copy(blob[ntidataHintGetProc+2:], "GetProcAddress\x00")
	copy(blob[ntidataHintLoadLib+2:], "LoadLibraryA\x00")
	copy(blob[ntidataDLLName:], "kernel32.dll\x00")
	// IAT slots hold the runtime's nonzero file-backed placeholders.
	binary.LittleEndian.PutUint64(elf[iatRVA:], 1)
	binary.LittleEndian.PutUint64(elf[iatRVA+8:], 1)

	return elf, &apePEInfo{
		entryRVA:   testELFEntry - peCosmoImageBase,
		importsRVA: idataRVA,
	}
}

// checkCosmoPEInvariants parses an APE with debug/pe and asserts the
// real amd64 header shape for the buildTestNTELF payload: sections
// mirroring the PT_LOADs (with the ELF-header page skipped and BSS as
// VirtualSize > SizeOfRawData), the entry inside .text, the fixed
// optional-header parameters, and the kernel32 import set.
func checkCosmoPEInvariants(t *testing.T, bin []byte) {
	t.Helper()
	f, err := pe.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("APE does not parse as PE: %v", err)
	}
	defer f.Close()

	if f.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		t.Errorf("machine = %#x, want amd64", f.Machine)
	}
	const wantChars = pe.IMAGE_FILE_RELOCS_STRIPPED | pe.IMAGE_FILE_EXECUTABLE_IMAGE |
		pe.IMAGE_FILE_LARGE_ADDRESS_AWARE | pe.IMAGE_FILE_DEBUG_STRIPPED
	if f.Characteristics != wantChars {
		t.Errorf("characteristics = %#x, want %#x", f.Characteristics, wantChars)
	}

	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		t.Fatalf("optional header is not PE32+")
	}
	if oh.ImageBase != peCosmoImageBase {
		t.Errorf("ImageBase = %#x, want %#x", oh.ImageBase, uint64(peCosmoImageBase))
	}
	if oh.AddressOfEntryPoint != testELFEntry-peCosmoImageBase {
		t.Errorf("entry = %#x, want %#x", oh.AddressOfEntryPoint, uint64(testELFEntry-peCosmoImageBase))
	}
	if oh.SectionAlignment != peCosmoSectAlign || oh.FileAlignment != peCosmoFileAlign {
		t.Errorf("alignment = %#x/%#x, want %#x/%#x", oh.SectionAlignment, oh.FileAlignment,
			peCosmoSectAlign, peCosmoFileAlign)
	}
	if oh.SizeOfHeaders != peCosmoHeadersSize {
		t.Errorf("SizeOfHeaders = %#x, want %#x", oh.SizeOfHeaders, peCosmoHeadersSize)
	}
	// Last load: off 0x4000, memsz 0x2800, rounded to section alignment.
	if want := uint32(0x7000); oh.SizeOfImage != want {
		t.Errorf("SizeOfImage = %#x, want %#x", oh.SizeOfImage, want)
	}
	if oh.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		t.Errorf("subsystem = %d, want console", oh.Subsystem)
	}
	const wantDllChars = pe.IMAGE_DLLCHARACTERISTICS_NX_COMPAT | pe.IMAGE_DLLCHARACTERISTICS_TERMINAL_SERVER_AWARE
	if oh.DllCharacteristics != wantDllChars {
		t.Errorf("DllCharacteristics = %#x, want %#x (no ASLR bits)", oh.DllCharacteristics, uint16(wantDllChars))
	}
	if oh.SizeOfStackReserve != 0x800000 || oh.SizeOfStackCommit != 0x10000 {
		t.Errorf("stack reserve/commit = %#x/%#x, want 0x800000/0x10000",
			oh.SizeOfStackReserve, oh.SizeOfStackCommit)
	}
	idd := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT]
	if idd.VirtualAddress != 0x4100 || idd.Size != peCosmoImportsSize {
		t.Errorf("import directory = {%#x, %#x}, want {0x4100, %#x}",
			idd.VirtualAddress, idd.Size, peCosmoImportsSize)
	}

	// Sections against testELFPhdrs: text load {off 0, filesz 0x2345}
	// minus its header page, R load {0x3000, 0x1000}, RW load {0x4000,
	// filesz 0x800, memsz 0x2800}. Raw pointers are absolute (payload at
	// apeHeaderSize); .data's raw size is filesz rounded to FileAlignment.
	want := []struct {
		name                 string
		rva, vsz, raw, rawsz uint32
		chars                uint32
	}{
		{".text", 0x1000, 0x2345 - 0x1000, apeHeaderSize + 0x1000, 0x2345 - 0x1000, 0x60000020},
		{".rodata", 0x3000, 0x1000, apeHeaderSize + 0x3000, 0x1000, 0x40000040},
		{".data", 0x4000, 0x2800, apeHeaderSize + 0x4000, 0x800, 0xC0000040},
	}
	if len(f.Sections) != len(want) {
		t.Fatalf("got %d sections, want %d", len(f.Sections), len(want))
	}
	for i, w := range want {
		s := f.Sections[i]
		if s.Name != w.name || s.VirtualAddress != w.rva || s.VirtualSize != w.vsz ||
			s.Offset != w.raw || s.Size != w.rawsz || s.Characteristics != w.chars {
			t.Errorf("section %d = %s va=%#x vsz=%#x raw=%#x rawsz=%#x chars=%#x,\nwant %s va=%#x vsz=%#x raw=%#x rawsz=%#x chars=%#x",
				i, s.Name, s.VirtualAddress, s.VirtualSize, s.Offset, s.Size, s.Characteristics,
				w.name, w.rva, w.vsz, w.raw, w.rawsz, w.chars)
		}
	}
	// BSS: .data declares more virtual space than raw bytes.
	if d := f.Sections[2]; d.VirtualSize <= d.Size {
		t.Errorf(".data VirtualSize %#x must exceed SizeOfRawData %#x (BSS zero-fill)", d.VirtualSize, d.Size)
	}
	// Entry inside .text.
	if txt := f.Sections[0]; oh.AddressOfEntryPoint < txt.VirtualAddress ||
		oh.AddressOfEntryPoint >= txt.VirtualAddress+txt.VirtualSize {
		t.Errorf("entry %#x outside .text [%#x, %#x)", oh.AddressOfEntryPoint,
			txt.VirtualAddress, txt.VirtualAddress+txt.VirtualSize)
	}

	syms, err := f.ImportedSymbols()
	if err != nil {
		t.Fatalf("ImportedSymbols: %v", err)
	}
	sort.Strings(syms)
	wantSyms := []string{"GetProcAddress:kernel32.dll", "LoadLibraryA:kernel32.dll"}
	if !slices.Equal(syms, wantSyms) {
		t.Errorf("imported symbols = %q, want %q", syms, wantSyms)
	}
}

// TestPECosmoHeaderStructure runs the real pipeline (payloadFromELF,
// writeAPEFile with an apePEInfo attached, as the thin link does after
// apePrepareNTBoot) on a synthetic payload and checks the emitted PE
// header with debug/pe.
func TestPECosmoHeaderStructure(t *testing.T) {
	elf, info := buildTestNTELF(t)
	p, err := payloadFromELF(elf)
	if err != nil {
		t.Fatal(err)
	}
	p.pe = info

	out := filepath.Join(t.TempDir(), "ape.com")
	writeAPEFile(out, []*apePayload{p})
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	checkCosmoPEInvariants(t, bin)
}

// TestAPEFatPETransplant runs the full fat chain: a thin APE with the
// real PE header is re-ingested by payloadFromAPEOrELF (capturing its
// head), merged with an arm64 payload, and the fat output must carry
// the thin header region byte for byte - valid as-is, since the amd64
// image lands at the same file offset with identical bytes.
func TestAPEFatPETransplant(t *testing.T) {
	elf, info := buildTestNTELF(t)
	p, err := payloadFromELF(elf)
	if err != nil {
		t.Fatal(err)
	}
	p.pe = info

	dir := t.TempDir()
	thinOut := filepath.Join(dir, "thin.com")
	writeAPEFile(thinOut, []*apePayload{p})
	thin, err := os.ReadFile(thinOut)
	if err != nil {
		t.Fatal(err)
	}

	amd, err := payloadFromAPEOrELF(thin)
	if err != nil {
		t.Fatalf("re-ingesting thin APE: %v", err)
	}
	if amd.head == nil {
		t.Fatalf("payloadFromAPEOrELF did not capture the input head")
	}

	armElf := buildTestELF(t, testELFEntry, testELFPhdrs())
	binary.LittleEndian.PutUint16(armElf[18:], elfMachineARM64)
	arm, err := payloadFromELF(armElf)
	if err != nil {
		t.Fatal(err)
	}

	fatOut := filepath.Join(dir, "fat.com")
	writeAPEFile(fatOut, []*apePayload{amd, arm})
	fat, err := os.ReadFile(fatOut)
	if err != nil {
		t.Fatal(err)
	}

	// The PE header region is transplanted verbatim (0x7FF stays the
	// forced pre-script newline in both).
	if !bytes.Equal(fat[0x80:apeScriptOffset], thin[0x80:apeScriptOffset]) {
		t.Errorf("fat header region [0x80, %#x) differs from the thin input's", apeScriptOffset)
	}
	if fat[apeScriptOffset-1] != '\n' {
		t.Errorf("byte before the script is %#x, want newline", fat[apeScriptOffset-1])
	}
	checkCosmoPEInvariants(t, fat)
}
