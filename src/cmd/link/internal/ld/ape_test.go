// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"encoding/binary"
	"internal/testenv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	binary.LittleEndian.PutUint16(elf[18:], elfMachineAMD64)
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
