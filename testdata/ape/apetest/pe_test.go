package apetest

import (
	"bytes"
	"debug/pe"
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
	assert.Greater(t, numSections, uint16(0), "must have sections")
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
	assert.Greater(t, imageBase, uint64(0x10000), "ImageBase must be above 64KB")
}

func TestPETextSection(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), 0x190)

	// .text section at 0x188
	name := string(bytes.TrimRight(bin[0x188:0x190], "\x00"))
	assert.Equal(t, ".text", name, "first section should be .text")
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
