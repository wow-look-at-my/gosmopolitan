package apetest

import (
	"bytes"
	"debug/macho"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const machoOffset = 0x1000

func TestMachoMagic(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+4)

	magic := le32(bin[machoOffset : machoOffset+4])
	assert.Equal(t, uint32(macho.Magic64), magic, "must have MH_MAGIC_64 at 0x1000")
}

func TestMachoCPUType(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+8)

	cpuType := le32(bin[machoOffset+4 : machoOffset+8])
	assert.Equal(t, uint32(macho.CpuAmd64), cpuType, "must be CPU_TYPE_X86_64")
}

func TestMachoCPUSubtype(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+12)

	// CPU_SUBTYPE_X86_64_ALL with LIB64 flag = 0x80000003
	subtype := le32(bin[machoOffset+8 : machoOffset+12])
	assert.Equal(t, uint32(0x80000003), subtype, "must be CPU_SUBTYPE_X86_64_ALL")
}

func TestMachoFileType(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+16)

	fileType := le32(bin[machoOffset+12 : machoOffset+16])
	assert.Equal(t, uint32(macho.TypeExec), fileType, "must be MH_EXECUTE")
}

func TestMachoNcmds(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+20)

	ncmds := le32(bin[machoOffset+16 : machoOffset+20])
	assert.Greater(t, ncmds, uint32(0), "must have load commands")
}

func TestMachoDdOffset(t *testing.T) {
	header := first8K(t)

	// Extract dd parameters
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match, "must have dd command")

	bs, _ := strconv.Atoi(string(match[1]))
	skip, _ := strconv.Atoi(string(match[2]))
	offset := bs * skip

	assert.Equal(t, machoOffset, offset, "dd skip*bs must equal Mach-O offset (0x1000)")
}

func TestMachoDdTransformProducesMagic(t *testing.T) {
	bin := loadBinary(t)
	header := first8K(t)

	// Extract dd parameters
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match)

	bs, _ := strconv.Atoi(string(match[1]))
	skip, _ := strconv.Atoi(string(match[2]))
	count, _ := strconv.Atoi(string(match[3]))
	srcOffset := bs * skip
	copySize := bs * count

	require.Greater(t, len(bin), srcOffset+copySize)

	// Simulate dd transform: copy from srcOffset to offset 0
	transformed := make([]byte, len(bin))
	copy(transformed, bin)
	copy(transformed[:copySize], bin[srcOffset:srcOffset+copySize])

	// Should now start with Mach-O magic
	magic := le32(transformed[:4])
	assert.Equal(t, uint32(macho.Magic64), magic, "transformed binary must start with MH_MAGIC_64")
}

func TestMachoParseable(t *testing.T) {
	bin := loadBinary(t)
	header := first8K(t)

	// Apply dd transform
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match)

	bs, _ := strconv.Atoi(string(match[1]))
	skip, _ := strconv.Atoi(string(match[2]))
	count, _ := strconv.Atoi(string(match[3]))
	srcOffset := bs * skip
	copySize := bs * count

	transformed := make([]byte, len(bin))
	copy(transformed, bin)
	copy(transformed[:copySize], bin[srcOffset:srcOffset+copySize])

	f, err := macho.NewFile(bytes.NewReader(transformed))
	require.NoError(t, err, "transformed binary must parse as Mach-O")
	defer f.Close()

	assert.Equal(t, macho.CpuAmd64, f.Cpu)
	assert.Equal(t, macho.TypeExec, f.Type)
}

func TestMachoHasSegments(t *testing.T) {
	bin := loadBinary(t)
	header := first8K(t)

	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match)

	bs, _ := strconv.Atoi(string(match[1]))
	skip, _ := strconv.Atoi(string(match[2]))
	count, _ := strconv.Atoi(string(match[3]))

	transformed := make([]byte, len(bin))
	copy(transformed, bin)
	copy(transformed[:bs*count], bin[bs*skip:bs*skip+bs*count])

	f, err := macho.NewFile(bytes.NewReader(transformed))
	require.NoError(t, err)
	defer f.Close()

	assert.Greater(t, len(f.Loads), 0, "must have load commands")

	var hasSegment bool
	for _, l := range f.Loads {
		if _, ok := l.(*macho.Segment); ok {
			hasSegment = true
			break
		}
	}
	assert.True(t, hasSegment, "must have segments")
}

func TestMachoUnixthread(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+0x70)

	// LC_UNIXTHREAD is at offset 0x1000 + 32 (header) + 72 (LC_SEGMENT_64) = 0x1068
	// cmd field
	cmd := le32(bin[machoOffset+0x68 : machoOffset+0x6C])
	const LC_UNIXTHREAD = 0x5
	assert.Equal(t, uint32(LC_UNIXTHREAD), cmd, "must have LC_UNIXTHREAD")
}

func TestMachoUnixthreadCmdsize(t *testing.T) {
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+0x70)

	// cmdsize at offset 0x106C
	cmdsize := le32(bin[machoOffset+0x6C : machoOffset+0x70])
	// 8 byte header + 8 byte flavor/count + 168 byte thread state = 184
	assert.Equal(t, uint32(184), cmdsize, "LC_UNIXTHREAD cmdsize should be 184")
}

func TestMachoEntryPointMatchesELF(t *testing.T) {
	bin := loadBinary(t)
	header := first8K(t)

	// Get ELF entry point
	elfData := extractELF(t)
	elfEntry := le64(elfData[24:32])

	// Get Mach-O entry point from LC_UNIXTHREAD
	// Apply dd transform first
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match)

	bs, _ := strconv.Atoi(string(match[1]))
	skip, _ := strconv.Atoi(string(match[2]))
	count, _ := strconv.Atoi(string(match[3]))

	transformed := make([]byte, len(bin))
	copy(transformed, bin)
	copy(transformed[:bs*count], bin[bs*skip:bs*skip+bs*count])

	f, err := macho.NewFile(bytes.NewReader(transformed))
	require.NoError(t, err)
	defer f.Close()

	// Entry point is in rip register at offset 16*8 = 128 in thread state
	// Thread state starts at offset 16 in LC_UNIXTHREAD (after cmd, cmdsize, flavor, count)
	// rip is register 16 (0-indexed)
	var machoEntry uint64
	for _, l := range f.Loads {
		if raw, ok := l.(macho.LoadBytes); ok {
			if len(raw) >= 4 && le32(raw[:4]) == 0x5 { // LC_UNIXTHREAD
				// rip at offset 16 + 16*8 = 144
				if len(raw) >= 152 {
					machoEntry = le64(raw[144:152])
				}
			}
		}
	}

	assert.Equal(t, elfEntry, machoEntry, "Mach-O and ELF entry points must match")
}
