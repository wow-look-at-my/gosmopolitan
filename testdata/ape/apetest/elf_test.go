package apetest

import (
	"bytes"
	"debug/elf"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestELFMagic(t *testing.T) {
	data := extractELF(t)

	assert.Equal(t, []byte{0x7f, 'E', 'L', 'F'}, data[:4], "must have ELF magic")
}

func TestELFClass64(t *testing.T) {
	data := extractELF(t)

	assert.Equal(t, byte(elf.ELFCLASS64), data[4], "must be 64-bit ELF")
}

func TestELFLittleEndian(t *testing.T) {
	data := extractELF(t)

	assert.Equal(t, byte(elf.ELFDATA2LSB), data[5], "must be little-endian")
}

func TestELFVersion(t *testing.T) {
	data := extractELF(t)

	assert.Equal(t, byte(elf.EV_CURRENT), data[6], "EI_VERSION must be EV_CURRENT")
}

func TestELFOSABI(t *testing.T) {
	data := extractELF(t)

	// Spec: OS ABI field SHOULD be set to ELFOSABI_FREEBSD
	assert.Equal(t, byte(elf.ELFOSABI_FREEBSD), data[7], "OS/ABI should be ELFOSABI_FREEBSD")
}

func TestELFTypeExec(t *testing.T) {
	data := extractELF(t)

	eType := le16(data[16:18])
	assert.Equal(t, uint16(elf.ET_EXEC), eType, "must be executable")
}

func TestELFMachineX86_64(t *testing.T) {
	data := extractELF(t)

	machine := le16(data[18:20])
	assert.Equal(t, uint16(elf.EM_X86_64), machine, "must be x86-64")
}

func TestELFHeaderSize(t *testing.T) {
	data := extractELF(t)

	ehsize := le16(data[52:54])
	assert.Equal(t, uint16(64), ehsize, "e_ehsize must be 64 bytes")
}

func TestELFPhentsize(t *testing.T) {
	data := extractELF(t)

	phentsize := le16(data[54:56])
	assert.Equal(t, uint16(56), phentsize, "e_phentsize must be 56 bytes")
}

func TestELFPhnum(t *testing.T) {
	data := extractELF(t)

	phnum := le16(data[56:58])
	assert.Greater(t, phnum, uint16(0), "must have at least one program header")
}

func TestELFEntryPointAbove4GB(t *testing.T) {
	data := extractELF(t)

	entry := le64(data[24:32])
	// Spec: can't load below 0x100000000 on ARM64 macOS
	assert.GreaterOrEqual(t, entry, uint64(0x100000000), "entry point must be >= 4GB")
}

func TestELFParseable(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err, "must parse as valid ELF")
	defer f.Close()

	assert.Equal(t, elf.ELFCLASS64, f.Class)
	assert.Equal(t, elf.EM_X86_64, f.Machine)
	assert.Equal(t, elf.ET_EXEC, f.Type)
}

func TestELFHasLoadSegments(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	var loadCount int
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			loadCount++
		}
	}
	assert.Greater(t, loadCount, 0, "must have PT_LOAD segments")
}

func TestELFHasExecutableSegment(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	var hasExec bool
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_X != 0 {
			hasExec = true
			break
		}
	}
	assert.True(t, hasExec, "must have executable segment")
}

func TestELFNoRWXSegments(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			isRWX := p.Flags&elf.PF_R != 0 && p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0
			assert.False(t, isRWX, "segment at 0x%x must not be RWX", p.Vaddr)
		}
	}
}

func TestELFHasTextSection(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	text := f.Section(".text")
	assert.NotNil(t, text, "must have .text section")
}

func TestELFHasDataSection(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	data_ := f.Section(".data")
	assert.NotNil(t, data_, "must have .data section")
}

func TestELFSegmentAlignment(t *testing.T) {
	data := extractELF(t)

	f, err := elf.NewFile(bytes.NewReader(data))
	require.NoError(t, err)
	defer f.Close()

	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			// Spec: alignment must be at least page size (4KB for x86-64)
			assert.GreaterOrEqual(t, p.Align, uint64(0x1000), "segment alignment must be >= 4KB")
		}
	}
}
