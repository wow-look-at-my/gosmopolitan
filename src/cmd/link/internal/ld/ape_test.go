// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
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

// TestHomeOrDefaultShellResolution runs `${TMPDIR:-homeOrDefault(dflt)}`
// through a real POSIX shell under the env combinations that matter,
// including HOME="/" -- what a container runtime hands a numeric --user
// UID with no /etc/passwd entry, confirmed directly against Docker. A
// plain `${HOME:-dflt}` treats that "/" as a real home and never reaches
// dflt; homeOrDefault must reject it the same as an absent HOME. Both of
// this fork's two call sites now pass "/tmp" -- apeRunDir, and the macOS
// ARM64 loader-cache path, which used to fall to "." (the caller's own
// current directory: unpredictable, and not guaranteed writable) instead.
func TestHomeOrDefaultShellResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh on windows")
	}
	testenv.MustHaveExecPath(t, "sh")

	cases := []struct {
		name    string
		home    string // "" leaves HOME unset
		tmpdir  string // "" leaves TMPDIR unset
		dflt    string
		want    string
	}{
		{"container UID with no passwd entry", "/", "", "/tmp", "/tmp"},
		{"neither set", "", "", "/tmp", "/tmp"},
		{"TMPDIR set wins over HOME", "/", "/custom", "/tmp", "/custom"},
		{"real per-user HOME", "/root", "", "/tmp", "/root"},
		{"a different dflt is honored verbatim", "/", "", "/var/cache", "/var/cache"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr := "${TMPDIR:-" + homeOrDefault(c.dflt) + "}"
			cmd := exec.Command("sh", "-c", `printf %s "`+expr+`"`)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			if c.home != "" {
				cmd.Env = append(cmd.Env, "HOME="+c.home)
			}
			if c.tmpdir != "" {
				cmd.Env = append(cmd.Env, "TMPDIR="+c.tmpdir)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sh -c failed: %v\n%s", err, out)
			}
			if got := string(out); got != c.want {
				t.Errorf("resolved to %q, want %q", got, c.want)
			}
		})
	}
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

// machoLoadCommands walks hdr's load commands, checking that the declared
// ncmds and sizeofcmds are consistent with the bytes actually present, and
// returns the raw commands. Any padding after the commands must be at most
// 7 bytes (the header is dd'd in 8-byte blocks).
func machoLoadCommands(t *testing.T, hdr []byte) [][]byte {
	t.Helper()
	if len(hdr) < 32 {
		t.Fatalf("Mach-O header too short: %d bytes", len(hdr))
	}
	ncmds := binary.LittleEndian.Uint32(hdr[16:20])
	sizeofcmds := binary.LittleEndian.Uint32(hdr[20:24])
	if want := 32 + int(sizeofcmds); len(hdr) < want {
		t.Fatalf("header is %d bytes, but mach header + sizeofcmds = %d", len(hdr), want)
	} else if pad := len(hdr) - want; pad >= 8 {
		t.Errorf("header has %d bytes of trailing padding, want < 8", pad)
	}
	var cmds [][]byte
	off := uint32(32)
	for i := uint32(0); i < ncmds; i++ {
		if off+8 > 32+sizeofcmds {
			t.Fatalf("load command %d at offset %d overruns sizeofcmds %d", i, off, sizeofcmds)
		}
		cmdsize := binary.LittleEndian.Uint32(hdr[off+4 : off+8])
		if cmdsize < 8 || off+cmdsize > 32+sizeofcmds {
			t.Fatalf("load command %d has cmdsize %d overrunning sizeofcmds %d", i, cmdsize, sizeofcmds)
		}
		cmds = append(cmds, hdr[off:off+cmdsize])
		off += cmdsize
	}
	if off != 32+sizeofcmds {
		t.Errorf("load commands end at offset %d, but sizeofcmds says %d", off, 32+sizeofcmds)
	}
	return cmds
}

// TestMachoUnixThreadState checks the LC_UNIXTHREAD command against the
// x86_THREAD_STATE64 layout the XNU kernel loads: exactly 21 register
// quadwords (cmdsize must match the bytes emitted - load_threadstack
// verifies (count+2)*4 bytes consume the command), rip at the ELF entry
// point, and the host-OS indicator for rt0_cosmo_amd64.s in rcx.
func TestMachoUnixThreadState(t *testing.T) {
	elf := buildTestELF(t, testELFEntry, testELFPhdrs())
	hdr := makeMachoHeader(elf, 0x10000, testELFEntry)

	var thread []byte
	for _, cmd := range machoLoadCommands(t, hdr) {
		if binary.LittleEndian.Uint32(cmd[0:4]) == machoLCUnixThread {
			if thread != nil {
				t.Fatalf("more than one LC_UNIXTHREAD")
			}
			thread = cmd
		}
	}
	if thread == nil {
		t.Fatalf("no LC_UNIXTHREAD load command")
	}

	if len(thread) != machoUnixThreadCmdSize {
		t.Errorf("LC_UNIXTHREAD is %d bytes, want %d", len(thread), machoUnixThreadCmdSize)
	}
	if got := binary.LittleEndian.Uint32(thread[4:8]); got != machoUnixThreadCmdSize {
		t.Errorf("LC_UNIXTHREAD cmdsize = %d, want %d", got, machoUnixThreadCmdSize)
	}
	if got := binary.LittleEndian.Uint32(thread[8:12]); got != machoThreadStateFlavor {
		t.Errorf("thread state flavor = %d, want %d (x86_THREAD_STATE64)", got, machoThreadStateFlavor)
	}
	if got := binary.LittleEndian.Uint32(thread[12:16]); got != machoThreadStateRegs*2 {
		t.Errorf("thread state count = %d, want %d", got, machoThreadStateRegs*2)
	}

	regs := thread[16:]
	if len(regs) != machoThreadStateRegs*8 {
		t.Fatalf("thread state has %d register bytes, want %d", len(regs), machoThreadStateRegs*8)
	}
	// rax rbx rcx rdx rdi rsi rbp rsp r8-r15 rip rflags cs fs gs
	for i := 0; i < machoThreadStateRegs; i++ {
		got := binary.LittleEndian.Uint64(regs[i*8 : i*8+8])
		var want uint64
		switch i {
		case 2: // rcx: host OS indicator (CL = 8 means XNU)
			want = machoHostXNU
		case 16: // rip
			want = testELFEntry
		}
		if got != want {
			t.Errorf("thread state register %d = %#x, want %#x", i, got, want)
		}
	}
}

// checkMachoKernelInvariants asserts the properties XNU's parse_machfile
// and load_segment demand of a kernel-loaded (dyld-less) executable:
//
//   - __PAGEZERO is the first segment and covers [0, lowest mapped vmaddr)
//     with no access.
//   - Exactly one segment maps file offset 0, and it is readable+executable
//     (parse_machfile's found_header_segment check).
//   - Segment file offsets and vm addresses are page-aligned, vm ranges do
//     not overlap, and filesize never exceeds vmsize.
//   - No segment is both writable and executable.
//   - The entry point (LC_UNIXTHREAD rip) falls inside an R+X segment
//     (parse_machfile's validentry check), and equals wantEntry.
func checkMachoKernelInvariants(t *testing.T, f *macho.File, wantEntry uint64) {
	t.Helper()

	if f.Cpu != macho.CpuAmd64 {
		t.Errorf("cpu = %v, want CpuAmd64", f.Cpu)
	}
	if f.Type != macho.TypeExec {
		t.Errorf("type = %v, want TypeExec", f.Type)
	}

	var segs []*macho.Segment
	for _, l := range f.Loads {
		if s, ok := l.(*macho.Segment); ok {
			segs = append(segs, s)
		}
	}
	if len(segs) < 2 {
		t.Fatalf("only %d segments, want __PAGEZERO plus at least one load", len(segs))
	}

	pz := segs[0]
	if pz.Name != "__PAGEZERO" {
		t.Errorf("first segment is %q, want __PAGEZERO", pz.Name)
	}
	if pz.Addr != 0 || pz.Offset != 0 || pz.Filesz != 0 || pz.Prot != 0 || pz.Maxprot != 0 {
		t.Errorf("__PAGEZERO addr=%#x off=%#x filesz=%#x prot=%#x/%#x, want all zero",
			pz.Addr, pz.Offset, pz.Filesz, pz.Maxprot, pz.Prot)
	}
	loads := segs[1:]
	if pz.Memsz != loads[0].Addr {
		t.Errorf("__PAGEZERO vmsize = %#x, want %#x (lowest mapped address)", pz.Memsz, loads[0].Addr)
	}

	const rx = machoProtRead | machoProtExec
	nHeaderSegs := 0
	for _, s := range loads {
		if s.Offset == 0 && s.Filesz > 0 {
			nHeaderSegs++
			if s.Prot&rx != rx {
				t.Errorf("segment %s maps file offset 0 but is not R+X (prot %#x)", s.Name, s.Prot)
			}
		}
		if s.Prot&(machoProtWrite|machoProtExec) == machoProtWrite|machoProtExec {
			t.Errorf("segment %s is both writable and executable (prot %#x)", s.Name, s.Prot)
		}
		if s.Prot != s.Maxprot {
			t.Errorf("segment %s initprot %#x != maxprot %#x", s.Name, s.Prot, s.Maxprot)
		}
		if s.Offset%machoPageSize != 0 || s.Addr%machoPageSize != 0 {
			t.Errorf("segment %s fileoff %#x / vmaddr %#x not page-aligned", s.Name, s.Offset, s.Addr)
		}
		if s.Filesz > s.Memsz {
			t.Errorf("segment %s filesize %#x > vmsize %#x", s.Name, s.Filesz, s.Memsz)
		}
	}
	if nHeaderSegs != 1 {
		t.Errorf("%d segments map file offset 0, XNU requires exactly 1", nHeaderSegs)
	}
	for i := 1; i < len(loads); i++ {
		if loads[i].Addr < loads[i-1].Addr+loads[i-1].Memsz {
			t.Errorf("segment %s (vmaddr %#x) overlaps %s (ends %#x)",
				loads[i].Name, loads[i].Addr, loads[i-1].Name, loads[i-1].Addr+loads[i-1].Memsz)
		}
	}

	entry, err := machoUnixThreadRip(f)
	if err != nil {
		t.Fatalf("LC_UNIXTHREAD: %v", err)
	}
	if entry != wantEntry {
		t.Errorf("LC_UNIXTHREAD rip = %#x, want %#x", entry, wantEntry)
	}
	entryOK := false
	for _, s := range loads {
		if entry >= s.Addr && entry < s.Addr+s.Memsz && s.Prot&rx == rx {
			entryOK = true
		}
	}
	if !entryOK {
		t.Errorf("entry point %#x is not inside an R+X segment", entry)
	}
}

// machoUnixThreadRip extracts rip from the (single) LC_UNIXTHREAD command.
func machoUnixThreadRip(f *macho.File) (uint64, error) {
	var rip uint64
	n := 0
	for _, l := range f.Loads {
		raw := l.Raw()
		if binary.LittleEndian.Uint32(raw[0:4]) != machoLCUnixThread {
			continue
		}
		n++
		if len(raw) != machoUnixThreadCmdSize {
			return 0, fmt.Errorf("LC_UNIXTHREAD is %d bytes, want %d", len(raw), machoUnixThreadCmdSize)
		}
		rip = binary.LittleEndian.Uint64(raw[16+16*8:])
	}
	if n != 1 {
		return 0, fmt.Errorf("found %d LC_UNIXTHREAD commands, want 1", n)
	}
	return rip, nil
}

// TestMachoHeaderStructure feeds makeMachoHeader a synthetic payload shaped
// like the cosmo linker's output, simulates the dd transform (header copied
// over offset 0), and checks the exact segment table against the program
// headers as well as the XNU kernel invariants.
func TestMachoHeaderStructure(t *testing.T) {
	const elfOff = 0x10000
	elf := buildTestELF(t, testELFEntry, testELFPhdrs())
	hdr := makeMachoHeader(elf, elfOff, testELFEntry)

	if len(hdr)%8 != 0 {
		t.Errorf("header length %d is not a multiple of the dd block size 8", len(hdr))
	}
	// The header is copied to apeMachoOffset in the APE header; the next embedded
	// artifact (the gzipped APE loader source) lives at 0x8000.
	if len(hdr) > apeLoaderSrcOffset-apeMachoOffset {
		t.Errorf("header is %d bytes, exceeding the %#x-%#x region", len(hdr), apeMachoOffset, apeLoaderSrcOffset)
	}

	// Simulate the dd transform on a synthetic APE image: the ELF payload
	// at its file offset, the Mach-O header copied over offset 0.
	img := make([]byte, elfOff+len(elf))
	copy(img[elfOff:], elf)
	copy(img, hdr)

	f, err := macho.NewFile(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("transformed image does not parse as Mach-O: %v", err)
	}
	defer f.Close()

	checkMachoKernelInvariants(t, f, testELFEntry)

	var segs []*macho.Segment
	for _, l := range f.Loads {
		if s, ok := l.(*macho.Segment); ok {
			segs = append(segs, s)
		}
	}
	// __PAGEZERO + one segment per PT_LOAD in testELFPhdrs (the PT_NOTE
	// must not produce a segment).
	want := []struct {
		name                     string
		addr, memsz, off, filesz uint64
		prot                     uint32
	}{
		{"__PAGEZERO", 0, 0x100000000 - elfOff, 0, 0, 0},
		// Text, extended down to file offset 0 to map the header.
		{"__TEXT", 0x100000000 - elfOff, 0x3000 + elfOff, 0, 0x2345 + elfOff, machoProtRead | machoProtExec},
		{"__RODATA", 0x100003000, 0x1000, elfOff + 0x3000, 0x1000, machoProtRead},
		// BSS: vmsize is p_memsz 0x2800 rounded up, exceeding filesize.
		{"__DATA", 0x100004000, 0x3000, elfOff + 0x4000, 0x800, machoProtRead | machoProtWrite},
	}
	if len(segs) != len(want) {
		t.Fatalf("got %d segments, want %d", len(segs), len(want))
	}
	for i, w := range want {
		s := segs[i]
		if s.Name != w.name || s.Addr != w.addr || s.Memsz != w.memsz ||
			s.Offset != w.off || s.Filesz != w.filesz || s.Prot != w.prot || s.Maxprot != w.prot {
			t.Errorf("segment %d = %s addr=%#x memsz=%#x off=%#x filesz=%#x prot=%#x/%#x,\nwant %s addr=%#x memsz=%#x off=%#x filesz=%#x prot=%#x",
				i, s.Name, s.Addr, s.Memsz, s.Offset, s.Filesz, s.Maxprot, s.Prot,
				w.name, w.addr, w.memsz, w.off, w.filesz, w.prot)
		}
	}
}

// apeDDParams extracts the bs/skip/count parameters of the Mach-O
// assimilation dd command from an APE header.
func apeDDParams(t *testing.T, header []byte) (bs, skip, count int) {
	t.Helper()
	m := regexp.MustCompile(`dd if="\$p\.\$\$" of="\$p\.\$\$" bs=(\d+) skip=(\d+) count=(\d+)`).FindSubmatch(header)
	if m == nil {
		t.Fatalf("no Mach-O dd command in APE header")
	}
	bs, _ = strconv.Atoi(string(m[1]))
	skip, _ = strconv.Atoi(string(m[2]))
	count, _ = strconv.Atoi(string(m[3]))
	return bs, skip, count
}

// TestAPEFileMachoTransform runs the real pipeline - payloadFromELF,
// writeAPEFile, the dd parameters from the generated bootstrap script - on a
// synthetic payload and verifies that the assimilated file is a Mach-O
// satisfying the kernel invariants, with segments that agree with the
// written (absolute) ELF program headers.
func TestAPEFileMachoTransform(t *testing.T) {
	elf := buildTestELF(t, testELFEntry, testELFPhdrs())
	p, err := payloadFromELF(elf)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "ape.com")
	writeAPEFile(out, []*apePayload{p})
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	bs, skip, count := apeDDParams(t, bin[:8192])
	if bs*skip != apeMachoOffset {
		t.Errorf("dd reads the Mach-O header from %#x, want %#x", bs*skip, apeMachoOffset)
	}
	if bs != 8 {
		t.Errorf("dd block size = %d, want 8", bs)
	}

	// The copied region must cover the whole emitted header (mach header
	// + sizeofcmds) with less than one block of padding.
	sizeofcmds := binary.LittleEndian.Uint32(bin[apeMachoOffset+20 : apeMachoOffset+24])
	hdrLen := 32 + int(sizeofcmds)
	if bs*count < hdrLen || bs*count >= hdrLen+8 {
		t.Errorf("dd copies %d bytes, want %d rounded up to a block", bs*count, hdrLen)
	}

	// Simulate the dd transform and parse.
	img := make([]byte, len(bin))
	copy(img, bin)
	copy(img[:bs*count], bin[bs*skip:bs*skip+bs*count])
	f, err := macho.NewFile(bytes.NewReader(img))
	if err != nil {
		t.Fatalf("assimilated APE does not parse as Mach-O: %v", err)
	}
	defer f.Close()

	checkMachoKernelInvariants(t, f, testELFEntry)

	// Cross-check the segment table against the written ELF's program
	// headers, whose p_offset values are now absolute file offsets.
	var segs []*macho.Segment
	for _, l := range f.Loads {
		if s, ok := l.(*macho.Segment); ok && s.Name != "__PAGEZERO" {
			segs = append(segs, s)
		}
	}
	phoff := binary.LittleEndian.Uint64(img[apeHeaderSize+32:])
	phnum := int(binary.LittleEndian.Uint16(img[apeHeaderSize+56:]))
	var loads []testProgHeader
	for i := 0; i < phnum; i++ {
		ph := img[apeHeaderSize:][phoff+uint64(i)*56:]
		if binary.LittleEndian.Uint32(ph[0:4]) != elfPTLoad {
			continue
		}
		loads = append(loads, testProgHeader{
			flags:  binary.LittleEndian.Uint32(ph[4:8]),
			off:    binary.LittleEndian.Uint64(ph[8:16]), // absolute
			vaddr:  binary.LittleEndian.Uint64(ph[16:24]),
			filesz: binary.LittleEndian.Uint64(ph[32:40]),
			memsz:  binary.LittleEndian.Uint64(ph[40:48]),
		})
	}
	if len(segs) != len(loads) {
		t.Fatalf("got %d load segments for %d PT_LOADs", len(segs), len(loads))
	}
	for i, ph := range loads {
		s := segs[i]
		wantProt := uint32(0)
		if ph.flags&elfPFR != 0 {
			wantProt |= machoProtRead
		}
		if ph.flags&elfPFW != 0 {
			wantProt |= machoProtWrite
		}
		if ph.flags&elfPFX != 0 {
			wantProt |= machoProtExec
		}
		wantOff, wantAddr := ph.off, ph.vaddr
		wantFilesz := ph.filesz
		wantMemsz := (ph.memsz + machoPageSize - 1) &^ uint64(machoPageSize-1)
		if i == 0 {
			// The first load is extended down to file offset 0.
			wantAddr -= wantOff
			wantFilesz += wantOff
			wantMemsz += wantOff
			wantOff = 0
		}
		if s.Offset != wantOff || s.Addr != wantAddr || s.Filesz != wantFilesz ||
			s.Memsz != wantMemsz || s.Prot != wantProt {
			t.Errorf("segment %s: off=%#x addr=%#x filesz=%#x memsz=%#x prot=%#x, want off=%#x addr=%#x filesz=%#x memsz=%#x prot=%#x (PT_LOAD %d)",
				s.Name, s.Offset, s.Addr, s.Filesz, s.Memsz, s.Prot,
				wantOff, wantAddr, wantFilesz, wantMemsz, wantProt, i)
		}
	}

	// BSS: the writable segment's vmsize must exceed its filesize.
	bss := false
	for _, s := range segs {
		if s.Prot&machoProtWrite != 0 && s.Memsz > s.Filesz {
			bss = true
		}
	}
	if !bss {
		t.Errorf("no writable segment with vmsize > filesize; BSS would not be zero-filled")
	}
}

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
