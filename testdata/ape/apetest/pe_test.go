// This file is the regression suite for the PE header every APE carries
// at offset 0x80. Since the Windows NT bring-up (wave 1) the header is
// real: PE32+ at ImageBase 0x100000000 whose sections map the embedded
// cosmo amd64 image directly (RVA == payload-relative file offset), whose
// import directory resolves kernel32!GetProcAddress + LoadLibraryA, and
// whose entry point is the runtime's _rt0_cosmo_nt boot stub (which
// marks the host as NT and joins the common cosmo runtime boot).
package apetest

import (
	"bytes"
	"debug/pe"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPEMZMagic(t *testing.T) {
	bin := loadBinary(t)

	assert.Equal(t, []byte{'M', 'Z'}, bin[:2], "must start with MZ")
}

func TestPELfanew(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x80)

	// e_lfanew at offset 0x3C points to PE header
	lfanew := le32(bin[0x3C:0x40])
	assert.Equal(t, uint32(0x80), lfanew, "e_lfanew should point to 0x80")
}

func TestPESignature(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x84)

	// PE\0\0 at offset pointed by e_lfanew
	assert.Equal(t, []byte{'P', 'E', 0, 0}, bin[0x80:0x84], "must have PE signature")
}

func TestPEMachineX86_64(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x86)

	machine := le16(bin[0x84:0x86])
	assert.Equal(t, uint16(pe.IMAGE_FILE_MACHINE_AMD64), machine, "must be x86-64")
}

func TestPEOptionalHeaderMagic(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x9A)

	// PE32+ magic at start of optional header (0x98)
	magic := le16(bin[0x98:0x9A])
	assert.Equal(t, uint16(0x20B), magic, "must be PE32+ (0x20B)")
}

func TestPESubsystem(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xDE)

	// Subsystem at optional header + 68 (0x44) = 0x98 + 0x44 = 0xDC
	subsystem := le16(bin[0xDC:0xDE])
	assert.Equal(t, uint16(pe.IMAGE_SUBSYSTEM_WINDOWS_CUI), subsystem, "must be console subsystem")
}

func TestPENumberOfSections(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x88)

	numSections := le16(bin[0x86:0x88])
	assert.Equal(t, uint16(3), numSections, "must have .text, .rodata, .data")
}

func TestPESizeOfOptionalHeader(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x96)

	size := le16(bin[0x94:0x96])
	assert.Equal(t, uint16(0xF0), size, "SizeOfOptionalHeader should be 240 for PE32+")
}

func TestPECharacteristicsExecutable(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x98)

	chars := le16(bin[0x96:0x98])
	assert.NotEqual(t, uint16(0), chars&pe.IMAGE_FILE_EXECUTABLE_IMAGE, "must have EXECUTABLE_IMAGE")
}

func TestPECharacteristicsLargeAddress(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x98)

	chars := le16(bin[0x96:0x98])
	assert.NotEqual(t, uint16(0), chars&pe.IMAGE_FILE_LARGE_ADDRESS_AWARE, "must have LARGE_ADDRESS_AWARE")
}

// TestPECharacteristicsExact pins the full COFF characteristics to the
// value real Cosmopolitan APEs use. RELOCS_STRIPPED matters: cosmo code
// is position-dependent and the image carries no .reloc section, so the
// header must tell the loader relocation is impossible.
func TestPECharacteristicsExact(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x98)

	chars := le16(bin[0x96:0x98])
	want := uint16(pe.IMAGE_FILE_RELOCS_STRIPPED | pe.IMAGE_FILE_EXECUTABLE_IMAGE |
		pe.IMAGE_FILE_LARGE_ADDRESS_AWARE | pe.IMAGE_FILE_DEBUG_STRIPPED)
	assert.Equal(t, want, chars, "COFF characteristics must be 0x0223")
}

// TestPEDllCharacteristicsNoASLR verifies ASLR is not invited: with
// relocations stripped, DYNAMIC_BASE or HIGH_ENTROPY_VA would let the
// loader move the position-dependent image off its link base.
func TestPEDllCharacteristicsNoASLR(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xE0)

	// DllCharacteristics at optional header + 70 = 0x98 + 0x46 = 0xDE
	dllChars := le16(bin[0xDE:0xE0])
	assert.Zero(t, dllChars&pe.IMAGE_DLLCHARACTERISTICS_DYNAMIC_BASE, "DYNAMIC_BASE must not be set")
	assert.Zero(t, dllChars&pe.IMAGE_DLLCHARACTERISTICS_HIGH_ENTROPY_VA, "HIGH_ENTROPY_VA must not be set")
	want := uint16(pe.IMAGE_DLLCHARACTERISTICS_NX_COMPAT | pe.IMAGE_DLLCHARACTERISTICS_TERMINAL_SERVER_AWARE)
	assert.Equal(t, want, dllChars, "DllCharacteristics must be 0x8100")
}

func TestPESectionAlignment(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xBC)

	// SectionAlignment at optional header + 32 = 0x98 + 0x20 = 0xB8
	alignment := le32(bin[0xB8:0xBC])
	assert.GreaterOrEqual(t, alignment, uint32(0x1000), "SectionAlignment must be >= 4KB")
}

func TestPEFileAlignment(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xC0)

	// Spec: PE requires at minimum 512 byte alignment
	// FileAlignment at optional header + 36 = 0x98 + 0x24 = 0xBC
	alignment := le32(bin[0xBC:0xC0])
	assert.GreaterOrEqual(t, alignment, uint32(512), "FileAlignment must be >= 512")
}

func TestPEImageBase(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xB8)

	// ImageBase at optional header + 24 = 0x98 + 0x18 = 0xB0
	imageBase := le64(bin[0xB0:0xB8])
	assert.Equal(t, uint64(0x100000000), imageBase, "ImageBase must be the cosmo amd64 link base")
}

func TestPESizeOfHeaders(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0xD8)

	// SizeOfHeaders at optional header + 60 = 0x98 + 0x3C = 0xD4
	size := le32(bin[0xD4:0xD8])
	assert.Equal(t, uint32(0x400), size, "SizeOfHeaders must cover the header chain, file-aligned")
}

func TestPETextSection(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x190)

	// .text section at 0x188
	name := string(bytes.TrimRight(bin[0x188:0x190], "\x00"))
	assert.Equal(t, ".text", name, "first section should be .text")
}

// TestPESectionsMapEmbeddedPayload verifies the sections map the real
// cosmo amd64 image: every section's raw data lives inside the embedded
// payload (at or beyond the 64K APE head), not in a stub inside the head.
func TestPESectionsMapEmbeddedPayload(t *testing.T) {
	bin := loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err)
	defer f.Close()

	require.Len(t, f.Sections, 3)
	var names []string
	for _, s := range f.Sections {
		names = append(names, s.Name)
		assert.GreaterOrEqual(t, s.Offset, uint32(elfOffset),
			"section %s raw data must live inside the embedded amd64 image", s.Name)
		assert.LessOrEqual(t, int64(s.Offset)+int64(s.Size), int64(len(bin)),
			"section %s raw data must not run past the file", s.Name)
	}
	assert.Equal(t, []string{".text", ".rodata", ".data"}, names)
}

// peEntry parses the binary and returns the entry point RVA plus the
// section containing it.
func peEntry(t *testing.T) (bin []byte, entry uint32, sect *pe.Section) {
	t.Helper()
	bin = loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err)
	defer f.Close()

	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	require.True(t, ok, "must use PE32+ optional header")
	entry = oh.AddressOfEntryPoint

	for _, s := range f.Sections {
		if entry >= s.VirtualAddress && entry-s.VirtualAddress < s.VirtualSize {
			return bin, entry, s
		}
	}
	t.Fatalf("entry point RVA %#x is not covered by any PE section", entry)
	return nil, 0, nil
}

// TestPEEntrypointInsideText verifies the entry point falls in .text's
// virtual range and is not the retired do-nothing placeholder stub.
func TestPEEntrypointInsideText(t *testing.T) {
	bin, entry, s := peEntry(t)

	assert.Equal(t, ".text", s.Name, "entry point must be inside .text")

	off := s.Offset + (entry - s.VirtualAddress)
	require.LessOrEqual(t, int(off)+3, len(bin), "entry point must be inside the file")
	assert.NotEqual(t, []byte{0x31, 0xC0, 0xC3}, bin[off:off+3],
		"entry point must not be the retired xor eax,eax; ret placeholder")
}

// TestPEEntrypointIsNTStub verifies the bytes at the entry point are the
// _rt0_cosmo_nt prologue: cld; fldcw m16 (rip-relative, so its 4
// displacement bytes vary); movl $2, m32 (the __hostos = _HOSTWINDOWS
// store that hands the host over to the runtime's NT personality -
// also rip-relative, 4 varying displacement bytes, then the immediate).
func TestPEEntrypointIsNTStub(t *testing.T) {
	bin, entry, s := peEntry(t)

	off := s.Offset + (entry - s.VirtualAddress)
	require.LessOrEqual(t, int(off)+17, len(bin), "entry prologue must be inside the file")
	prologue := bin[off : off+17]
	assert.Equal(t, byte(0xFC), prologue[0], "entry must start with cld")
	assert.Equal(t, []byte{0xD9, 0x2D}, prologue[1:3], "cld must be followed by fldcw m16 (x87 re-init)")
	assert.Equal(t, []byte{0xC7, 0x05}, prologue[7:9], "fldcw must be followed by movl $imm32, m32 (the __hostos store)")
	assert.Equal(t, []byte{0x02, 0x00, 0x00, 0x00}, prologue[13:17], "__hostos immediate must be 2 (_HOSTWINDOWS)")
}

// TestPEImportsKernel32 verifies the loader-resolved import set: exactly
// one import descriptor, naming kernel32.dll, importing GetProcAddress
// and LoadLibraryA (everything else is resolved at runtime through
// those two).
func TestPEImportsKernel32(t *testing.T) {
	bin := loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err)
	defer f.Close()

	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	require.True(t, ok, "must use PE32+ optional header")
	idd := oh.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_IMPORT]
	assert.NotZero(t, idd.VirtualAddress, "import directory must be present")
	assert.Equal(t, uint32(0x28), idd.Size, "import directory must hold one descriptor plus the terminator")

	syms, err := f.ImportedSymbols()
	require.NoError(t, err)
	sort.Strings(syms)
	assert.Equal(t, []string{"GetProcAddress:kernel32.dll", "LoadLibraryA:kernel32.dll"}, syms)
}

// TestPEDataSectionHasBSS verifies .data declares more virtual space
// than raw data, making the loader zero-fill the runtime's BSS.
func TestPEDataSectionHasBSS(t *testing.T) {
	bin := loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err)
	defer f.Close()

	data := f.Section(".data")
	require.NotNil(t, data, "must have a .data section")
	assert.Greater(t, data.VirtualSize, data.Size,
		".data VirtualSize must exceed SizeOfRawData so the loader zero-fills BSS")
}

func TestPETextSectionExecutable(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x1B0)

	// Characteristics at 0x188 + 36 = 0x1AC
	chars := le32(bin[0x1AC:0x1B0])
	const IMAGE_SCN_MEM_EXECUTE = 0x20000000
	assert.NotEqual(t, uint32(0), chars&IMAGE_SCN_MEM_EXECUTE, ".text must be executable")
}

func TestPETextSectionReadable(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x1B0)

	chars := le32(bin[0x1AC:0x1B0])
	const IMAGE_SCN_MEM_READ = 0x40000000
	assert.NotEqual(t, uint32(0), chars&IMAGE_SCN_MEM_READ, ".text must be readable")
}

func TestPETextSectionNotWritable(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x1B0)

	chars := le32(bin[0x1AC:0x1B0])
	const IMAGE_SCN_MEM_WRITE = 0x80000000
	assert.Equal(t, uint32(0), chars&IMAGE_SCN_MEM_WRITE, ".text must not be writable")
}

func TestPEParseable(t *testing.T) {
	bin := loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err, "must parse as valid PE")
	defer f.Close()

	assert.EqualValues(t, pe.IMAGE_FILE_MACHINE_AMD64, f.Machine)
}

func TestPEHasSections(t *testing.T) {
	bin := loadBinary(t)

	f, err := pe.NewFile(bytes.NewReader(bin))
	require.NoError(t, err)
	defer f.Close()

	assert.Greater(t, len(f.Sections), 0, "must have sections")

	var sectionNames []string
	for _, s := range f.Sections {
		sectionNames = append(sectionNames, s.Name)
	}
	assert.Contains(t, sectionNames, ".text", "must have .text section")
}

func TestPEPolyglotMagic(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 8)

	// MZ magic also forms shell variable assignment: MZqFpD='
	assert.Equal(t, byte('='), bin[6], "byte 6 must be '=' for shell assignment")
	assert.Equal(t, byte('\''), bin[7], "byte 7 must be single quote")
}
