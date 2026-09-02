package apetest

import (
	"bytes"
	"debug/macho"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const machoOffset = 0x2000

// machoDDParams extracts the bs/skip/count of the Mach-O assimilation dd
// command from the bootstrap script.
// hasMacho reports whether this APE carries the Mach-O header at all. The
// header boots the amd64 payload on macOS INTEL. macOS arm64 does not use it
// - it boots through the compiled APE loader - so an APE that does not claim
// darwin/amd64 carries no Mach-O header, and the default selection does not
// claim it. Build with GOCOSMOPLATFORMS=darwin/amd64 to get one.
func hasMacho(t *testing.T) bool {
	t.Helper()
	bin := loadBinary(t)
	if len(bin) < machoOffset+4 {
		return false
	}
	return le32(bin[machoOffset:machoOffset+4]) == uint32(macho.Magic64)
}

// skipWithoutMacho skips a test that reads the Mach-O header when this APE
// has none. TestMachoAbsentUnlessDarwinAMD64 asserts the absence itself, so a
// silently header-less build cannot pass by skipping alone.
func skipWithoutMacho(t *testing.T) {
	t.Helper()
	if !hasMacho(t) {
		t.Skip("no Mach-O header: this APE does not claim darwin/amd64")
	}
}

// The Mach-O header is present exactly when the APE claims darwin/amd64.
// This asserts the pairing in the direction the skips above cannot: a build
// that quietly stopped emitting a header it should emit fails here.
func TestMachoAbsentUnlessDarwinAMD64(t *testing.T) {
	claimed := strings.Contains(string(first8K(t)), "darwin/amd64")
	assert.Equal(t, claimed, hasMacho(t),
		"a Mach-O header must be present exactly when the APE claims darwin/amd64")
}

func machoDDParams(t *testing.T) (bs, skip, count int) {
	t.Helper()
	skipWithoutMacho(t)
	header := first8K(t)
	ddPattern := regexp.MustCompile(`dd\s+if=.*of=.*bs=(\d+)\s+skip=(\d+)\s+count=(\d+)`)
	match := ddPattern.FindSubmatch(header)
	require.NotNil(t, match, "must have dd command")
	bs, _ = strconv.Atoi(string(match[1]))
	skip, _ = strconv.Atoi(string(match[2]))
	count, _ = strconv.Atoi(string(match[3]))
	return bs, skip, count
}

// machoTransform simulates the macOS Intel dd assimilation: the Mach-O
// header region is copied over the start of the file.
func machoTransform(t *testing.T) []byte {
	t.Helper()
	skipWithoutMacho(t)
	bin := loadBinary(t)
	bs, skip, count := machoDDParams(t)
	require.Greater(t, len(bin), bs*(skip+count))

	transformed := make([]byte, len(bin))
	copy(transformed, bin)
	copy(transformed[:bs*count], bin[bs*skip:bs*skip+bs*count])
	return transformed
}

// machoFile parses the dd-transformed binary as a Mach-O.
func machoFile(t *testing.T) *macho.File {
	t.Helper()
	f, err := macho.NewFile(bytes.NewReader(machoTransform(t)))
	require.NoError(t, err, "transformed binary must parse as Mach-O")
	t.Cleanup(func() { f.Close() })
	return f
}

// machoSegments returns the Mach-O segments in load-command order.
func machoSegments(t *testing.T) []*macho.Segment {
	t.Helper()
	var segs []*macho.Segment
	for _, l := range machoFile(t).Loads {
		if s, ok := l.(*macho.Segment); ok {
			segs = append(segs, s)
		}
	}
	require.NotEmpty(t, segs, "must have segments")
	return segs
}

// machoUnixThread locates the LC_UNIXTHREAD command by walking the load
// commands (its position in the header is not fixed).
func machoUnixThread(t *testing.T) []byte {
	t.Helper()
	const LC_UNIXTHREAD = 0x5
	var thread []byte
	for _, l := range machoFile(t).Loads {
		raw := l.Raw()
		if len(raw) >= 4 && le32(raw[:4]) == LC_UNIXTHREAD {
			require.Nil(t, thread, "must have exactly one LC_UNIXTHREAD")
			thread = raw
		}
	}
	require.NotNil(t, thread, "must have LC_UNIXTHREAD")
	return thread
}

// x86_THREAD_STATE64 register offsets within LC_UNIXTHREAD: 16 bytes of
// cmd/cmdsize/flavor/count, then rax rbx rcx rdx rdi rsi rbp rsp r8-r15
// rip rflags cs fs gs as quadwords.
const (
	threadStateRcxOff = 16 + 2*8
	threadStateRipOff = 16 + 16*8
)

// elfProgHeader is one program header of the embedded ELF payload. The
// on-disk payload's p_offset values are absolute APE file offsets.
type elfProgHeader struct {
	flags  uint32
	off    uint64
	vaddr  uint64
	filesz uint64
	memsz  uint64
}

// elfLoadSegments returns the PT_LOAD program headers of the embedded
// amd64 ELF payload.
func elfLoadSegments(t *testing.T) []elfProgHeader {
	t.Helper()
	elfData := extractELF(t)
	phoff := le64(elfData[32:40])
	phentsize := uint64(le16(elfData[54:56]))
	phnum := int(le16(elfData[56:58]))
	require.Equal(t, uint64(56), phentsize, "e_phentsize")

	var loads []elfProgHeader
	for i := 0; i < phnum; i++ {
		ph := elfData[phoff+uint64(i)*phentsize:]
		const PT_LOAD = 1
		if le32(ph[0:4]) != PT_LOAD {
			continue
		}
		loads = append(loads, elfProgHeader{
			flags:  le32(ph[4:8]),
			off:    le64(ph[8:16]),
			vaddr:  le64(ph[16:24]),
			filesz: le64(ph[32:40]),
			memsz:  le64(ph[40:48]),
		})
	}
	require.NotEmpty(t, loads, "embedded ELF must have PT_LOAD headers")
	return loads
}

func TestMachoMagic(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+4)

	magic := le32(bin[machoOffset : machoOffset+4])
	assert.Equal(t, uint32(macho.Magic64), magic, "must have MH_MAGIC_64 at 0x1000")
}

func TestMachoCPUType(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+8)

	cpuType := le32(bin[machoOffset+4 : machoOffset+8])
	assert.Equal(t, uint32(macho.CpuAmd64), cpuType, "must be CPU_TYPE_X86_64")
}

func TestMachoCPUSubtype(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+12)

	// CPU_SUBTYPE_X86_64_ALL with LIB64 flag = 0x80000003
	subtype := le32(bin[machoOffset+8 : machoOffset+12])
	assert.Equal(t, uint32(0x80000003), subtype, "must be CPU_SUBTYPE_X86_64_ALL")
}

func TestMachoFileType(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+16)

	fileType := le32(bin[machoOffset+12 : machoOffset+16])
	assert.Equal(t, uint32(macho.TypeExec), fileType, "must be MH_EXECUTE")
}

func TestMachoNcmds(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	require.Greater(t, len(bin), machoOffset+20)

	ncmds := le32(bin[machoOffset+16 : machoOffset+20])
	assert.Greater(t, ncmds, uint32(0), "must have load commands")
}

func TestMachoDdOffset(t *testing.T) {
	bs, skip, _ := machoDDParams(t)
	assert.Equal(t, machoOffset, bs*skip, "dd skip*bs must equal Mach-O offset (0x1000)")
}

// TestMachoDdCountCoversHeader checks that the dd copy covers exactly the
// emitted Mach-O header (mach header + sizeofcmds), with less than one dd
// block of padding.
func TestMachoDdCountCoversHeader(t *testing.T) {
	skipWithoutMacho(t)
	bin := loadBinary(t)
	bs, _, count := machoDDParams(t)

	sizeofcmds := le32(bin[machoOffset+20 : machoOffset+24])
	hdrLen := 32 + int(sizeofcmds)
	assert.GreaterOrEqual(t, bs*count, hdrLen, "dd copy must cover the whole Mach-O header")
	assert.Less(t, bs*count, hdrLen+bs, "dd copy must not extend a full block past the header")
}

func TestMachoDdTransformProducesMagic(t *testing.T) {
	transformed := machoTransform(t)

	// Should now start with Mach-O magic
	magic := le32(transformed[:4])
	assert.Equal(t, uint32(macho.Magic64), magic, "transformed binary must start with MH_MAGIC_64")
}

func TestMachoParseable(t *testing.T) {
	f := machoFile(t)
	assert.Equal(t, macho.CpuAmd64, f.Cpu)
	assert.Equal(t, macho.TypeExec, f.Type)
}

func TestMachoHasSegments(t *testing.T) {
	machoSegments(t)
}

// TestMachoPageZeroFirst checks the leading __PAGEZERO segment: XNU raises
// the process's minimum vm offset to its end, so it must cover everything
// below the first mapped segment.
func TestMachoPageZeroFirst(t *testing.T) {
	segs := machoSegments(t)
	require.GreaterOrEqual(t, len(segs), 2, "need __PAGEZERO plus mapped segments")

	pz := segs[0]
	assert.Equal(t, "__PAGEZERO", pz.Name, "first segment must be __PAGEZERO")
	assert.Zero(t, pz.Addr, "__PAGEZERO vmaddr")
	assert.Zero(t, pz.Offset, "__PAGEZERO fileoff")
	assert.Zero(t, pz.Filesz, "__PAGEZERO filesize")
	assert.Zero(t, pz.Prot, "__PAGEZERO initprot")
	assert.Zero(t, pz.Maxprot, "__PAGEZERO maxprot")
	assert.Equal(t, segs[1].Addr, pz.Memsz, "__PAGEZERO must end at the first mapped address")
}

// TestMachoHeaderSegmentMapsFileStart checks XNU's found_header_segment
// requirement: exactly one segment maps file offset 0, and it must be R+X.
func TestMachoHeaderSegmentMapsFileStart(t *testing.T) {
	const rx = 0x1 | 0x4 // VM_PROT_READ | VM_PROT_EXECUTE
	n := 0
	for _, s := range machoSegments(t) {
		if s.Offset == 0 && s.Filesz > 0 {
			n++
			assert.Equal(t, uint32(rx), s.Prot&rx, "segment %s maps file offset 0 and must be R+X", s.Name)
		}
	}
	assert.Equal(t, 1, n, "exactly one segment must map file offset 0")
}

// TestMachoSegmentProtections checks per-segment protections: never W+X,
// initprot == maxprot, and page-aligned mappings (XNU rejects unaligned
// segment file offsets and vm addresses).
func TestMachoSegmentProtections(t *testing.T) {
	const (
		vmProtWrite = 0x2
		vmProtExec  = 0x4
		pageSize    = 0x1000
	)
	segs := machoSegments(t)
	for _, s := range segs[1:] {
		assert.NotEqual(t, uint32(vmProtWrite|vmProtExec), s.Prot&(vmProtWrite|vmProtExec),
			"segment %s must not be writable+executable", s.Name)
		assert.Equal(t, s.Maxprot, s.Prot, "segment %s initprot must equal maxprot", s.Name)
		assert.Zero(t, s.Offset%pageSize, "segment %s fileoff must be page-aligned", s.Name)
		assert.Zero(t, s.Addr%pageSize, "segment %s vmaddr must be page-aligned", s.Name)
		assert.LessOrEqual(t, s.Filesz, s.Memsz, "segment %s filesize must not exceed vmsize", s.Name)
	}
}

// TestMachoNoVMOverlap checks that segment vm ranges do not overlap (XNU's
// vm_map_enter would fail the second mapping).
func TestMachoNoVMOverlap(t *testing.T) {
	segs := machoSegments(t)
	for i := 1; i < len(segs); i++ {
		prevEnd := segs[i-1].Addr + segs[i-1].Memsz
		assert.GreaterOrEqual(t, segs[i].Addr, prevEnd,
			"segment %s overlaps %s", segs[i].Name, segs[i-1].Name)
	}
}

// TestMachoBSSZeroFill checks that the writable segment's vmsize exceeds
// its filesize: XNU zero-fills [filesize, vmsize), which is where the Go
// runtime's BSS lives.
func TestMachoBSSZeroFill(t *testing.T) {
	const vmProtWrite = 0x2
	found := false
	for _, s := range machoSegments(t) {
		if s.Prot&vmProtWrite != 0 && s.Memsz > s.Filesz {
			found = true
		}
	}
	assert.True(t, found, "a writable segment with vmsize > filesize (BSS) is required")
}

// TestMachoSegmentsMatchELFPhdrs cross-checks the Mach-O segment table
// against the embedded ELF's PT_LOAD program headers: one segment per
// PT_LOAD at the same address with translated protections, the first
// extended down to file offset 0 to map the APE/Mach-O header.
func TestMachoSegmentsMatchELFPhdrs(t *testing.T) {
	const (
		pfX      = 0x1
		pfW      = 0x2
		pfR      = 0x4
		pageSize = 0x1000
	)
	segs := machoSegments(t)[1:] // skip __PAGEZERO
	loads := elfLoadSegments(t)
	require.Equal(t, len(loads), len(segs), "one Mach-O segment per PT_LOAD")

	for i, ph := range loads {
		s := segs[i]
		var wantProt uint32
		if ph.flags&pfR != 0 {
			wantProt |= 0x1
		}
		if ph.flags&pfW != 0 {
			wantProt |= 0x2
		}
		if ph.flags&pfX != 0 {
			wantProt |= 0x4
		}
		wantOff, wantAddr, wantFilesz := ph.off, ph.vaddr, ph.filesz
		wantMemsz := (ph.memsz + pageSize - 1) &^ uint64(pageSize-1)
		if i == 0 {
			// Extended down to file offset 0.
			wantAddr -= wantOff
			wantFilesz += wantOff
			wantMemsz += wantOff
			wantOff = 0
		}
		assert.Equal(t, wantOff, s.Offset, "segment %s fileoff (PT_LOAD %d)", s.Name, i)
		assert.Equal(t, wantAddr, s.Addr, "segment %s vmaddr (PT_LOAD %d)", s.Name, i)
		assert.Equal(t, wantFilesz, s.Filesz, "segment %s filesize (PT_LOAD %d)", s.Name, i)
		assert.Equal(t, wantMemsz, s.Memsz, "segment %s vmsize (PT_LOAD %d)", s.Name, i)
		assert.Equal(t, wantProt, s.Prot, "segment %s protection (PT_LOAD %d)", s.Name, i)
	}
}

// TestMachoUnixthread checks the LC_UNIXTHREAD command shape: the declared
// cmdsize must equal both the bytes present and the fixed x86_THREAD_STATE64
// size (16 header bytes + 21 register quadwords = 184; XNU's
// load_threadstack validates that (count+2)*4 bytes consume the command).
func TestMachoUnixthread(t *testing.T) {
	thread := machoUnixThread(t)

	assert.Equal(t, 184, len(thread), "LC_UNIXTHREAD must be 184 bytes")
	assert.Equal(t, uint32(184), le32(thread[4:8]), "LC_UNIXTHREAD cmdsize")
	assert.Equal(t, uint32(4), le32(thread[8:12]), "flavor must be x86_THREAD_STATE64")
	assert.Equal(t, uint32(42), le32(thread[12:16]), "count must be 42 32-bit words (21 registers)")
}

// TestMachoRcxHostOS checks that the thread state hands the XNU host-OS
// indicator (8) to the entry point in rcx: rt0_cosmo_amd64.s reads CL to
// decide between Linux and Apple syscall ABIs, and a kernel-loaded Mach-O
// that identified as Linux would die on its first syscall.
func TestMachoRcxHostOS(t *testing.T) {
	thread := machoUnixThread(t)
	require.GreaterOrEqual(t, len(thread), threadStateRcxOff+8)

	rcx := le64(thread[threadStateRcxOff : threadStateRcxOff+8])
	assert.Equal(t, uint64(8), rcx, "rcx must carry the XNU host-OS indicator")
}

func TestMachoEntryPointMatchesELF(t *testing.T) {
	// Get ELF entry point
	elfData := extractELF(t)
	elfEntry := le64(elfData[24:32])

	thread := machoUnixThread(t)
	require.GreaterOrEqual(t, len(thread), threadStateRipOff+8)
	machoEntry := le64(thread[threadStateRipOff : threadStateRipOff+8])

	assert.Equal(t, elfEntry, machoEntry, "Mach-O and ELF entry points must match")
}

// TestMachoEntryInExecutableSegment checks XNU's validentry requirement:
// the entry point must fall inside a segment mapped readable+executable.
func TestMachoEntryInExecutableSegment(t *testing.T) {
	const rx = 0x1 | 0x4
	thread := machoUnixThread(t)
	entry := le64(thread[threadStateRipOff : threadStateRipOff+8])

	found := false
	for _, s := range machoSegments(t) {
		if entry >= s.Addr && entry < s.Addr+s.Memsz && s.Prot&rx == rx {
			found = true
		}
	}
	assert.True(t, found, "entry point %#x must be inside an R+X segment", entry)
}
