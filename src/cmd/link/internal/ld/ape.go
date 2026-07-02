// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
)

// APE (Actually Portable Executable) format implementation
// Based on the specification at: ape/specification.md
//
// APE creates polyglot executables that work on multiple OSes:
// - Windows: Uses PE header starting with MZ magic
// - Linux: Uses embedded ELF header (encoded as octal in printf)
// - macOS: Uses dd command to copy Mach-O header backward (ARM64 uses Rosetta2)
// - Windows shell (MSYS/Cygwin): Delegates to cmd.exe for PE execution

const (
	// APE header must be page-aligned for ELF loading
	// Using 64KB for Windows allocation granularity compatibility
	apeHeaderSize   = 65536
	apeScriptOffset = 0x800

	// Page sizes
	pageSize4K  = 4096
	pageSize16K = 16384

	// ELF constants
	elfMagic        = "\x7fELF"
	elfClass64      = 2
	elfDataLSB      = 1
	elfOSABIFreeBSD = 9 // Use FreeBSD ABI per spec
	elfTypeExec     = 2
	elfMachineAMD64 = 0x3E
	elfMachineARM64 = 0xB7

	// Mach-O constants
	machoMagic64       = 0xFEEDFACF
	machoCPUTypeX64    = 0x01000007
	machoCPUSubtypeX64 = 0x80000003
	machoFileTypeExec  = 0x2
	machoFlagNoUndefs  = 0x1
	machoFlagPIE       = 0x200000

	// Load commands
	machoLCSegment64  = 0x19
	machoLCUnixThread = 0x5
	machoLCMain       = 0x80000028

	// Segment protection
	machoProtRead  = 0x1
	machoProtWrite = 0x2
	machoProtExec  = 0x4
)

// convertToAPE converts an ELF binary to Actually Portable Executable format.
// apePayload describes one architecture's ELF image embedded in an APE file.
type apePayload struct {
	elf    []byte // complete ELF image; p_offset values are payload-relative
	arch   sys.ArchFamily
	offset uint64 // file offset of this image inside the APE; set by layoutAPE
}

// pePayload describes a native Windows PE image embedded in an APE file.
// The final APE's PE header points its sections at this payload's section
// bodies, shifted by offset.
type pePayload struct {
	pe     []byte
	arch   sys.ArchFamily
	offset uint64
}

// payloadFromELF validates elf and wraps it as an APE payload.
func payloadFromELF(elf []byte) (*apePayload, error) {
	if len(elf) < 64 || string(elf[0:4]) != elfMagic {
		return nil, fmt.Errorf("not a valid ELF binary")
	}
	var arch sys.ArchFamily
	switch m := binary.LittleEndian.Uint16(elf[18:20]); m {
	case elfMachineAMD64:
		arch = sys.AMD64
	case elfMachineARM64:
		arch = sys.ARM64
	default:
		return nil, fmt.Errorf("unsupported ELF machine type %#x", m)
	}
	return &apePayload{elf: elf, arch: arch}, nil
}

// payloadFromPE validates pe and wraps it as a Windows payload.
func payloadFromPE(pe []byte) (*pePayload, error) {
	peoff, err := peHeaderOffset(pe)
	if err != nil {
		return nil, err
	}
	if len(pe) < peoff+24 {
		return nil, fmt.Errorf("truncated PE COFF header")
	}
	machine := binary.LittleEndian.Uint16(pe[peoff+4:])
	var arch sys.ArchFamily
	switch machine {
	case 0x8664:
		arch = sys.AMD64
	case 0xAA64:
		arch = sys.ARM64
	default:
		return nil, fmt.Errorf("unsupported PE machine type %#x", machine)
	}
	return &pePayload{pe: pe, arch: arch}, nil
}

func (p *apePayload) entry() uint64 { return binary.LittleEndian.Uint64(p.elf[24:32]) }

func (ctxt *Link) convertToAPE() {
	if ctxt.HeadType != objabi.Hcosmo {
		return
	}

	outfile := *flagOutfile
	if outfile == "" {
		return
	}

	// Read the ELF file we just created
	elfData, err := os.ReadFile(outfile)
	if err != nil {
		Exitf("cannot read output file for APE conversion: %v", err)
	}
	p, err := payloadFromELF(elfData)
	if err != nil {
		Exitf("APE conversion: %v", err)
	}
	if p.arch != ctxt.Arch.Family {
		Exitf("APE conversion: ELF machine type does not match link architecture")
	}
	writeAPEFile(outfile, []*apePayload{p}, nil)
}

// apePayloadAlign is the alignment of payload images within the APE file.
// The APE loader requires p_vaddr to be congruent to p_offset modulo 16384
// for every program header; placing payloads on 64K boundaries (the largest
// page size in play) preserves whatever congruence each image already has.
const apePayloadAlign = 0x10000

// layoutAPE assigns file offsets to the payloads: the first begins right
// after the APE header, each subsequent payload at the next aligned boundary.
func layoutAPE(payloads []*apePayload, win *pePayload) {
	off := uint64(apeHeaderSize)
	for _, p := range payloads {
		p.offset = off
		off += uint64(len(p.elf))
		off = (off + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)
	}
	if win != nil {
		win.offset = off
	}
}

// writeAPEFile writes an APE polyglot containing the given payloads.
// Payload p_offset values are rewritten to absolute file offsets.
func writeAPEFile(outfile string, payloads []*apePayload, win *pePayload) {
	layoutAPE(payloads, win)
	header := makeAPEHeaderForPayloads(payloads, win)

	apeFile, err := os.Create(outfile)
	if err != nil {
		Exitf("cannot create APE output: %v", err)
	}
	defer apeFile.Close()

	if _, err := apeFile.Write(header); err != nil {
		Exitf("cannot write APE header: %v", err)
	}
	cur := uint64(apeHeaderSize)
	for _, p := range payloads {
		if p.offset > cur {
			if _, err := apeFile.Write(make([]byte, p.offset-cur)); err != nil {
				Exitf("cannot write APE padding: %v", err)
			}
			cur = p.offset
		}
		if _, err := apeFile.Write(shiftPOffsets(p.elf, p.offset)); err != nil {
			Exitf("cannot write APE payload: %v", err)
		}
		cur += uint64(len(p.elf))
	}
	if win != nil {
		if win.offset > cur {
			if _, err := apeFile.Write(make([]byte, win.offset-cur)); err != nil {
				Exitf("cannot write APE padding: %v", err)
			}
			cur = win.offset
		}
		if _, err := apeFile.Write(win.pe); err != nil {
			Exitf("cannot write Windows PE payload: %v", err)
		}
	}

	if err := os.Chmod(outfile, 0755); err != nil {
		Exitf("cannot chmod APE output: %v", err)
	}
}

// shiftPOffsets returns a copy of elf whose program header p_offset values
// are increased by delta, making them absolute within the APE file.
func shiftPOffsets(elf []byte, delta uint64) []byte {
	out := make([]byte, len(elf))
	copy(out, elf)
	phoff := binary.LittleEndian.Uint64(out[32:40])
	phentsize := binary.LittleEndian.Uint16(out[54:56])
	phnum := binary.LittleEndian.Uint16(out[56:58])
	for i := uint16(0); i < phnum; i++ {
		ph := phoff + uint64(i)*uint64(phentsize)
		pOffset := binary.LittleEndian.Uint64(out[ph+8:])
		binary.LittleEndian.PutUint64(out[ph+8:], pOffset+delta)
	}
	return out
}

// writePrintfBlob escapes blob into script as the body of a shell
// printf '...' statement: printable ASCII stays literal, everything else
// becomes an octal escape. Single quotes must be octal too -- not the shell
// backslash-quote idiom -- because the APE loader's printf decoder stops at the first
// raw quote byte when it scans the header for embedded boot ELF headers.
// Percent signs must be octal as well: printf would treat a bare '%' in its
// format string as a conversion directive, corrupting the self-assimilation
// write whenever a variable header byte (e_entry, e_phoff, ...) is 0x25.
func writePrintfBlob(script *bytes.Buffer, blob []byte) {
	for _, b := range blob {
		if b >= 0x20 && b < 0x7f && b != '\\' && b != '\'' && b != '%' {
			script.WriteByte(b)
		} else {
			fmt.Fprintf(script, "\\%03o", b)
		}
	}
}

// makeAPEHeaderForPayloads creates the 64K APE polyglot header that boots
// the given payloads (at most one per architecture family). With both an
// amd64 and an arm64 payload the result is a fat APE: the bootstrap script
// and the embedded boot headers dispatch on the host architecture, and the
// macOS ARM64 APE loader finds the aarch64 image by decoding every printf
// statement in the first 8192 bytes.
func makeAPEHeaderForPayloads(payloads []*apePayload, win *pePayload) []byte {
	var amd, arm *apePayload
	for _, p := range payloads {
		switch p.arch {
		case sys.AMD64:
			if amd != nil {
				Exitf("APE: more than one amd64 payload")
			}
			amd = p
		case sys.ARM64:
			if arm != nil {
				Exitf("APE: more than one arm64 payload")
			}
			arm = p
		default:
			Exitf("APE: unsupported payload architecture")
		}
	}
	if amd == nil && arm == nil {
		Exitf("APE: no payloads")
	}

	header := make([]byte, apeHeaderSize)

	// Embedded (printf-encoded) boot ELF headers. They serve two purposes:
	// self-assimilation on Linux, and discovery by the macOS ARM64 APE
	// loader, which octal-decodes every printf in the first 8192 bytes and
	// uses the first one with an aarch64 machine type.
	var amdBoot, armBoot []byte
	if amd != nil {
		amdBoot = makeEmbeddedElfHeader(amd.elf, amd.offset, pageSize4K, sys.AMD64)
	}
	if arm != nil {
		armBoot = makeEmbeddedElfHeader(arm.elf, arm.offset, pageSize16K, sys.ARM64)
	}

	// Create Mach-O header for macOS x86-64
	var machoHeader []byte
	var machoOffset, machoSize int
	if amd != nil {
		machoHeader = makeMachoHeader(amd.elf, amd.offset, amd.entry())
		// Place Mach-O header at a specific location in the APE header.
		// It will be copied backward by the dd command.
		machoOffset = 0x1000 // 4KB into the header
		machoSize = len(machoHeader)
	}

	// Load gzipped APE loader source for macOS ARM64
	var apeLoaderGz []byte
	var apeLoaderOffset, apeLoaderSize int
	if arm != nil {
		apeLoaderGz = getApeLoaderSource()
		if len(apeLoaderGz) > 0 {
			// Place gzipped loader at offset 0x8000 (32KB into header).
			// This leaves room for the script.
			apeLoaderOffset = 0x8000
			apeLoaderSize = len(apeLoaderGz)
		}
	}

	// === Build the APE header with shell script ===
	//
	// The APE format is a polyglot that must satisfy:
	// 1. DOS/PE: Starts with "MZ", e_lfanew at 0x3C points to PE header at 0x80
	// 2. Shell: Valid shell script that can run on UNIX systems
	//
	// CRITICAL: The e_lfanew field at 0x3C contains null bytes (0x80 0x00 0x00 0x00).
	// Bash cannot handle null bytes in the shell-parsed portion of a script.
	// Solution: Start the heredoc BEFORE 0x3C so null bytes are in heredoc body.
	//
	// Structure:
	// - Bytes 0x00-0x07: "MZqFpD='" - DOS magic + shell variable start
	// - Byte 0x08: newline (inside quoted string)
	// - Bytes 0x09-0x2B: spaces (inside quoted string, 35 bytes)
	// - Byte 0x2C: "'" - close the quoted string
	// - Bytes 0x2D-0x3B: "\n: <<'__APE__'\n" - heredoc opener (15 bytes)
	// - Bytes 0x3C+: heredoc body (contains e_lfanew with null bytes - SAFE!)
	// - PE header at 0x80 (inside heredoc body)
	// - Script at apeScriptOffset starts with "__APE__\n" to terminate heredoc

	// Write the APE magic at offset 0
	copy(header[0:8], []byte("MZqFpD='"))
	header[8] = '\n'

	// Fill bytes 0x09-0x2B with spaces (inside the single-quoted string)
	for i := 0x09; i < 0x2C; i++ {
		header[i] = ' '
	}

	// Close the quoted string at 0x2C
	header[0x2C] = '\''

	// Heredoc opener at 0x2D-0x3B (15 bytes: "\n: <<'__APE__'\n")
	// The trailing newline ends the heredoc opener line.
	// Heredoc body starts at 0x3C.
	heredocOpener := []byte("\n: <<'__APE__'\n")
	copy(header[0x2D:], heredocOpener)

	// Now 0x3C+ is heredoc body - null bytes are safe here!
	// e_lfanew at 0x3C-0x3F - must point to PE header at 0x80
	binary.LittleEndian.PutUint32(header[0x3C:], 0x80)

	// Fill bytes 0x40-0x7F with safe content (heredoc body)
	// Use printable characters to avoid any shell parsing issues
	for i := 0x40; i < 0x80; i++ {
		header[i] = '#'
	}

	// The script starts after the transplanted PE headers.
	var script bytes.Buffer

	// Here-doc terminator
	script.WriteString("__APE__\n")

	// Architecture dispatch
	script.WriteString("m=$(uname -m 2>/dev/null) || m=x86_64\n")

	// --- x86-64 hosts ---
	script.WriteString("if [ \"$m\" = x86_64 ] || [ \"$m\" = amd64 ]; then\n")
	if amd != nil {
		script.WriteString(`  o="$(command -v "$0")"
  exec 7<> "$o" || exit 121
  printf '`)
		writePrintfBlob(&script, amdBoot)
		script.WriteString("' >&7\n")
		script.WriteString("  exec 7<&-\n")
		if machoSize > 0 {
			bs := 8
			skip := machoOffset / bs
			count := (machoSize + bs - 1) / bs
			fmt.Fprintf(&script, "  if [ -d /Applications ]; then\n")
			fmt.Fprintf(&script, "    dd if=\"$o\" of=\"$o\" bs=%d skip=%d count=%d conv=notrunc 2>/dev/null || { echo 'APE: Mach-O assimilation failed' >&2; exit 121; }\n", bs, skip, count)
			fmt.Fprintf(&script, "  fi\n")
		}
		script.WriteString("  exec \"$0\" \"$@\"\n")
	} else {
		script.WriteString("  echo 'APE: x86_64 cannot run ARM64 binary' >&2\n")
		script.WriteString("  exit 1\n")
	}
	script.WriteString("fi\n")

	// --- ARM64 hosts ---
	script.WriteString("if [ \"$m\" = aarch64 ] || [ \"$m\" = arm64 ]; then\n")
	if arm != nil {
		script.WriteString(`  o="$(command -v "$0")"
  t="${TMPDIR:-${HOME:-.}}/.ape-1.10"
  if [ -d /Applications ]; then
    # macOS ARM64: use compiled Mach-O loader or compile from source
    # Don't use existing loader if it might be ELF (from Linux)
    if [ -x "$t" ] && file "$t" 2>/dev/null | grep -q "Mach-O"; then
      exec "$t" "$o" "$@"
    fi
    # Compile APE loader from embedded source
    if ! type cc >/dev/null 2>&1; then
      echo "$0: please run: xcode-select --install" >&2
      exit 1
    fi
    mkdir -p "${t%/*}" || exit
    dd if="$o" bs=1 skip=APE_LOADER_OFFSET count=APE_LOADER_SIZE 2>/dev/null | gzip -dc >"$t.c.$$" || exit
    mv -f "$t.c.$$" "$t.c" || exit
    cc -w -O -o "$t.$$" "$t.c" || exit
    mv -f "$t.$$" "$t" || exit
    exec "$t" "$o" "$@"
  fi
  # Linux ARM64: prefer an installed loader, else self-assimilate
  type ape >/dev/null 2>&1 && exec ape "$o" "$@"
  [ -x "$t" ] && exec "$t" "$o" "$@"
  exec 7<> "$o" || exit 121
  printf '`)
		writePrintfBlob(&script, armBoot)
		script.WriteString("' >&7\n")
		script.WriteString("  exec 7<&-\n")
		script.WriteString("  exec \"$0\" \"$@\"\n")
	} else {
		// Note for this branch: it cannot work on current macOS even via
		// Rosetta. The assimilated Mach-O fails codesign's strict
		// validation (verified on macOS 15.7: "main executable failed
		// strict validation"), and Apple Silicon SIGKILLs unsigned
		// executables. Native ARM64 macOS execution requires an arm64
		// payload (fat APE) via the compiled APE loader path.
		script.WriteString(`  if [ -d /Applications ]; then
    echo 'APE: this amd64-only binary cannot run natively on ARM64 macOS.' >&2
    echo 'APE: rebuild with ARM64 (fat APE) support to run on Apple Silicon.' >&2
    exit 1
  fi
  echo 'APE: ARM64 Linux cannot run x86_64 binary' >&2
  exit 1
`)
	}
	script.WriteString("fi\n")

	script.WriteString(`# Windows shells (MSYS/Cygwin): delegate to cmd.exe for PE execution
case "$(uname -s 2>/dev/null)" in
CYGWIN*|MINGW*|MSYS*) exec cmd //c "$0" "$@" ;;
esac
echo 'APE: unsupported platform' >&2
exit 1
`)

	scriptBytes := script.Bytes()

	// Replace APE loader offset/size placeholders for the macOS ARM64 path
	if arm != nil && apeLoaderSize > 0 {
		s := string(scriptBytes)
		s = replaceAll(s, "APE_LOADER_OFFSET", fmt.Sprintf("%d", apeLoaderOffset))
		s = replaceAll(s, "APE_LOADER_SIZE", fmt.Sprintf("%d", apeLoaderSize))
		scriptBytes = []byte(s)
	}

	// Place script after the PE headers.
	scriptOffset := apeScriptOffset
	if len(scriptBytes) > apeHeaderSize-scriptOffset {
		Exitf("APE shell script too large: %d bytes", len(scriptBytes))
	}
	// The Mach-O header and APE loader are copied over the header after the
	// script; if the script has grown into their regions they would silently
	// clobber its tail, leaving a binary that parses as a broken shell script.
	if machoSize > 0 && scriptOffset+len(scriptBytes) > machoOffset {
		Exitf("APE shell script (%d bytes at %#x) overlaps Mach-O header at %#x", len(scriptBytes), scriptOffset, machoOffset)
	}
	if apeLoaderSize > 0 && scriptOffset+len(scriptBytes) > apeLoaderOffset {
		Exitf("APE shell script (%d bytes at %#x) overlaps APE loader at %#x", len(scriptBytes), scriptOffset, apeLoaderOffset)
	}
	// The APE loader scans only the first 8192 bytes for printf statements;
	// every boot header must decode from within that window.
	if scriptOffset+len(scriptBytes) > 8192 {
		Exitf("APE shell script ends at %#x, beyond the loader's 8192-byte scan window", scriptOffset+len(scriptBytes))
	}
	copy(header[scriptOffset:], scriptBytes)

	// Embed gzipped APE loader source for macOS ARM64
	if apeLoaderSize > 0 {
		if apeLoaderOffset+apeLoaderSize > apeHeaderSize {
			Exitf("APE loader too large to embed: %d bytes at offset %d", apeLoaderSize, apeLoaderOffset)
		}
		copy(header[apeLoaderOffset:], apeLoaderGz)
	}

	// === PE Header at offset 0x80 ===
	// Required for Windows support. Prefer a real Windows payload when one
	// was supplied; otherwise keep the legacy parseable stub for single-arch
	// cosmo-only builds.
	if win != nil {
		writePEHeaderFromPayload(header, win)
	} else {
		peArch := sys.AMD64
		if amd == nil {
			peArch = sys.ARM64
		}
		writePEHeader(header, peArch)
	}

	// === Mach-O header for macOS x86-64 ===
	if machoSize > 0 && machoOffset+machoSize <= apeHeaderSize {
		copy(header[machoOffset:], machoHeader)
	}

	// Ensure there's a newline before the script (required for heredoc terminator)
	// The __APE__ at the start of the script must be at the beginning of a line
	if scriptOffset > 0 {
		header[scriptOffset-1] = '\n'
	}

	// Pad remainder with newlines (safe for shell parsing)
	// Start after the script ends, but skip embedded data regions
	scriptEnd := scriptOffset + len(scriptBytes)
	for i := scriptEnd; i < apeHeaderSize; i++ {
		// Don't overwrite the Mach-O header with newlines
		if machoSize > 0 && i >= machoOffset && i < machoOffset+machoSize {
			continue
		}
		// Don't overwrite the APE loader data with newlines
		if apeLoaderSize > 0 && i >= apeLoaderOffset && i < apeLoaderOffset+apeLoaderSize {
			continue
		}
		if header[i] == 0 {
			header[i] = '\n'
		}
	}

	return header
}

// makeEmbeddedElfHeader creates an ELF header for embedding in the APE printf statement.
// This header points to the actual ELF segments in the APE file.
func makeEmbeddedElfHeader(origElf []byte, elfOffset uint64, pageSize uint64, arch sys.ArchFamily) []byte {
	// Create a minimal ELF header (64 bytes for ELF64)
	hdr := make([]byte, 64)

	// ELF magic
	copy(hdr[0:4], elfMagic)
	hdr[4] = elfClass64      // 64-bit
	hdr[5] = elfDataLSB      // Little endian
	hdr[6] = 1               // ELF version
	hdr[7] = elfOSABIFreeBSD // FreeBSD ABI per spec

	// Object file type
	binary.LittleEndian.PutUint16(hdr[16:], elfTypeExec)

	// Machine type
	switch arch {
	case sys.ARM64:
		binary.LittleEndian.PutUint16(hdr[18:], elfMachineARM64)
	default:
		binary.LittleEndian.PutUint16(hdr[18:], elfMachineAMD64)
	}

	// ELF version
	binary.LittleEndian.PutUint32(hdr[20:], 1)

	// Entry point - copy from original
	copy(hdr[24:32], origElf[24:32])

	// Program header offset - adjusted for APE header
	phoff := binary.LittleEndian.Uint64(origElf[32:40])
	binary.LittleEndian.PutUint64(hdr[32:], phoff+elfOffset)

	// Section header offset (set to 0, not used for execution)
	binary.LittleEndian.PutUint64(hdr[40:], 0)

	// Flags
	binary.LittleEndian.PutUint32(hdr[48:], 0)

	// ELF header size
	binary.LittleEndian.PutUint16(hdr[52:], 64)

	// Program header entry size and count - copy from original
	copy(hdr[54:56], origElf[54:56]) // e_phentsize
	copy(hdr[56:58], origElf[56:58]) // e_phnum

	// Section header entry size and count (not used)
	binary.LittleEndian.PutUint16(hdr[58:], 64)
	binary.LittleEndian.PutUint16(hdr[60:], 0)
	binary.LittleEndian.PutUint16(hdr[62:], 0)

	return hdr
}

// makeMachoHeader creates a Mach-O header for macOS x86-64.
func makeMachoHeader(elfData []byte, elfOffset uint64, elfEntry uint64) []byte {
	// Find ELF base virtual address from the first PT_LOAD segment
	// ELF program header offset is at byte 32, entry size at 54, count at 56
	elfPhoff := binary.LittleEndian.Uint64(elfData[32:40])
	elfPhentsize := binary.LittleEndian.Uint16(elfData[54:56])
	elfPhnum := binary.LittleEndian.Uint16(elfData[56:58])

	var elfBaseVAddr uint64
	for i := uint16(0); i < elfPhnum; i++ {
		phdr := elfData[elfPhoff+uint64(i)*uint64(elfPhentsize):]
		pType := binary.LittleEndian.Uint32(phdr[0:4])
		if pType == 1 { // PT_LOAD
			elfBaseVAddr = binary.LittleEndian.Uint64(phdr[16:24]) // p_vaddr
			break
		}
	}

	// Mach-O loads at this virtual address
	const machoVMAddr = uint64(0x100000000)

	// Calculate the Mach-O entry point
	// The ELF entry is relative to elfBaseVAddr, so adjust for machoVMAddr
	machoEntry := machoVMAddr + (elfEntry - elfBaseVAddr)

	var buf bytes.Buffer

	// Mach-O header (32 bytes)
	binary.Write(&buf, binary.LittleEndian, uint32(machoMagic64))       // magic
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUTypeX64))    // cputype
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUSubtypeX64)) // cpusubtype
	binary.Write(&buf, binary.LittleEndian, uint32(machoFileTypeExec))  // filetype
	binary.Write(&buf, binary.LittleEndian, uint32(2))                  // ncmds (LC_SEGMENT_64 + LC_UNIXTHREAD)
	binary.Write(&buf, binary.LittleEndian, uint32(72+184))             // sizeofcmds
	binary.Write(&buf, binary.LittleEndian, uint32(machoFlagNoUndefs))  // flags
	binary.Write(&buf, binary.LittleEndian, uint32(0))                  // reserved

	// LC_SEGMENT_64 for __TEXT (72 bytes)
	binary.Write(&buf, binary.LittleEndian, uint32(machoLCSegment64))            // cmd
	binary.Write(&buf, binary.LittleEndian, uint32(72))                          // cmdsize
	buf.WriteString("__TEXT\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")            // segname (16 bytes)
	binary.Write(&buf, binary.LittleEndian, machoVMAddr)                         // vmaddr
	binary.Write(&buf, binary.LittleEndian, uint64(len(elfData)))                // vmsize
	binary.Write(&buf, binary.LittleEndian, elfOffset)                           // fileoff
	binary.Write(&buf, binary.LittleEndian, uint64(len(elfData)))                // filesize
	binary.Write(&buf, binary.LittleEndian, uint32(machoProtRead|machoProtExec)) // maxprot
	binary.Write(&buf, binary.LittleEndian, uint32(machoProtRead|machoProtExec)) // initprot
	binary.Write(&buf, binary.LittleEndian, uint32(0))                           // nsects
	binary.Write(&buf, binary.LittleEndian, uint32(0))                           // flags

	// LC_UNIXTHREAD (184 bytes for x86_64)
	binary.Write(&buf, binary.LittleEndian, uint32(machoLCUnixThread)) // cmd
	binary.Write(&buf, binary.LittleEndian, uint32(184))               // cmdsize
	binary.Write(&buf, binary.LittleEndian, uint32(4))                 // flavor (x86_THREAD_STATE64)
	binary.Write(&buf, binary.LittleEndian, uint32(42))                // count

	// Thread state (42 uint64 values = 336 bytes, but we only write key ones)
	// Registers: rax, rbx, rcx, rdx, rdi, rsi, rbp, rsp, r8-r15, rip, rflags, cs, fs, gs
	for i := 0; i < 16; i++ {
		binary.Write(&buf, binary.LittleEndian, uint64(0)) // rax through r15
	}
	binary.Write(&buf, binary.LittleEndian, machoEntry) // rip (entry point)
	binary.Write(&buf, binary.LittleEndian, uint64(0))  // rflags
	for i := 0; i < 4; i++ {
		binary.Write(&buf, binary.LittleEndian, uint64(0)) // cs, fs, gs, etc.
	}

	return buf.Bytes()
}

func peHeaderOffset(pe []byte) (int, error) {
	if len(pe) < 0x40 || string(pe[0:2]) != "MZ" {
		return 0, fmt.Errorf("not a valid PE binary")
	}
	peoff := int(binary.LittleEndian.Uint32(pe[0x3C:]))
	if peoff < 0 || len(pe) < peoff+4 || string(pe[peoff:peoff+4]) != "PE\x00\x00" {
		return 0, fmt.Errorf("not a valid PE binary")
	}
	return peoff, nil
}

// writePEHeaderFromPayload transplants a native Windows PE header into the
// APE header. Section RVAs and data-directory RVAs remain unchanged; section
// raw file offsets are shifted to point into the embedded PE payload.
func writePEHeaderFromPayload(header []byte, win *pePayload) {
	const peStart = 0x80
	peoff, err := peHeaderOffset(win.pe)
	if err != nil {
		Exitf("Windows PE payload: %v", err)
	}
	coffStart := peoff + 4
	if len(win.pe) < coffStart+20 {
		Exitf("Windows PE payload: truncated COFF header")
	}
	numSections := int(binary.LittleEndian.Uint16(win.pe[coffStart+2:]))
	sizeOpt := int(binary.LittleEndian.Uint16(win.pe[coffStart+16:]))
	optStart := coffStart + 20
	sectStart := optStart + sizeOpt
	headersEnd := sectStart + numSections*40
	if len(win.pe) < headersEnd {
		Exitf("Windows PE payload: truncated section table")
	}
	if peStart+(headersEnd-peoff) > apeScriptOffset {
		Exitf("Windows PE payload: PE headers too large for APE script gap")
	}
	if win.offset > uint64(^uint32(0)) {
		Exitf("Windows PE payload: file offset %#x exceeds PE32 raw pointer range", win.offset)
	}

	copy(header[peStart:], win.pe[peoff:headersEnd])

	newCoffStart := peStart + 4
	newSizeOpt := int(binary.LittleEndian.Uint16(header[newCoffStart+16:]))
	newOptStart := newCoffStart + 20
	newSectStart := newOptStart + newSizeOpt
	symptr := binary.LittleEndian.Uint32(header[newCoffStart+8:])
	if symptr != 0 {
		shifted := uint64(symptr) + win.offset
		if shifted > uint64(^uint32(0)) {
			Exitf("Windows PE payload: shifted symbol table pointer %#x exceeds PE32 range", shifted)
		}
		binary.LittleEndian.PutUint32(header[newCoffStart+8:], uint32(shifted))
	}
	binary.LittleEndian.PutUint32(header[newOptStart+64:], 0) // CheckSum
	for i := 0; i < numSections; i++ {
		sect := newSectStart + i*40
		raw := binary.LittleEndian.Uint32(header[sect+20:])
		if raw == 0 {
			continue
		}
		shifted := uint64(raw) + win.offset
		if shifted > uint64(^uint32(0)) {
			Exitf("Windows PE payload: shifted section raw pointer %#x exceeds PE32 range", shifted)
		}
		binary.LittleEndian.PutUint32(header[sect+20:], uint32(shifted))
	}
}

// writePEHeader writes the PE header for Windows support.
func writePEHeader(header []byte, arch sys.ArchFamily) {
	peStart := 0x80

	// PE Signature
	copy(header[peStart:], []byte{'P', 'E', 0, 0})

	// COFF Header
	coffStart := peStart + 4
	var machineType uint16
	switch arch {
	case sys.ARM64:
		machineType = 0xAA64
	default:
		machineType = 0x8664
	}
	binary.LittleEndian.PutUint16(header[coffStart+0:], machineType)
	binary.LittleEndian.PutUint16(header[coffStart+2:], 1)     // NumberOfSections
	binary.LittleEndian.PutUint32(header[coffStart+4:], 0)     // TimeDateStamp
	binary.LittleEndian.PutUint32(header[coffStart+8:], 0)     // PointerToSymbolTable
	binary.LittleEndian.PutUint32(header[coffStart+12:], 0)    // NumberOfSymbols
	binary.LittleEndian.PutUint16(header[coffStart+16:], 240)  // SizeOfOptionalHeader
	binary.LittleEndian.PutUint16(header[coffStart+18:], 0x22) // Characteristics

	// Optional Header (PE32+)
	optStart := coffStart + 20
	binary.LittleEndian.PutUint16(header[optStart+0:], 0x20B)        // Magic: PE32+
	header[optStart+2] = 1                                           // MajorLinkerVersion
	header[optStart+3] = 0                                           // MinorLinkerVersion
	binary.LittleEndian.PutUint32(header[optStart+4:], 0x200)        // SizeOfCode
	binary.LittleEndian.PutUint32(header[optStart+8:], 0)            // SizeOfInitializedData
	binary.LittleEndian.PutUint32(header[optStart+12:], 0)           // SizeOfUninitializedData
	binary.LittleEndian.PutUint32(header[optStart+16:], 0x1000)      // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(header[optStart+20:], 0x1000)      // BaseOfCode
	binary.LittleEndian.PutUint64(header[optStart+24:], 0x140000000) // ImageBase
	binary.LittleEndian.PutUint32(header[optStart+32:], 0x1000)      // SectionAlignment
	binary.LittleEndian.PutUint32(header[optStart+36:], 0x200)       // FileAlignment
	binary.LittleEndian.PutUint16(header[optStart+40:], 6)           // MajorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+42:], 0)           // MinorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+44:], 0)           // MajorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+46:], 0)           // MinorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+48:], 6)           // MajorSubsystemVersion
	binary.LittleEndian.PutUint16(header[optStart+50:], 0)           // MinorSubsystemVersion
	binary.LittleEndian.PutUint32(header[optStart+52:], 0)           // Win32VersionValue
	binary.LittleEndian.PutUint32(header[optStart+56:], 0x2000)      // SizeOfImage
	binary.LittleEndian.PutUint32(header[optStart+60:], 0x200)       // SizeOfHeaders
	binary.LittleEndian.PutUint32(header[optStart+64:], 0)           // CheckSum
	binary.LittleEndian.PutUint16(header[optStart+68:], 3)           // Subsystem: CONSOLE
	binary.LittleEndian.PutUint16(header[optStart+70:], 0x8160)      // DllCharacteristics
	binary.LittleEndian.PutUint64(header[optStart+72:], 0x100000)    // SizeOfStackReserve
	binary.LittleEndian.PutUint64(header[optStart+80:], 0x1000)      // SizeOfStackCommit
	binary.LittleEndian.PutUint64(header[optStart+88:], 0x100000)    // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(header[optStart+96:], 0x1000)      // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(header[optStart+104:], 0)          // LoaderFlags
	binary.LittleEndian.PutUint32(header[optStart+108:], 16)         // NumberOfRvaAndSizes

	// Section Header
	sectStart := optStart + 240
	copy(header[sectStart:], []byte(".text\x00\x00\x00"))
	binary.LittleEndian.PutUint32(header[sectStart+8:], 0x1000)
	binary.LittleEndian.PutUint32(header[sectStart+12:], 0x1000)
	binary.LittleEndian.PutUint32(header[sectStart+16:], 0x200)
	binary.LittleEndian.PutUint32(header[sectStart+20:], 0x200)
	binary.LittleEndian.PutUint32(header[sectStart+24:], 0)
	binary.LittleEndian.PutUint32(header[sectStart+28:], 0)
	binary.LittleEndian.PutUint16(header[sectStart+32:], 0)
	binary.LittleEndian.PutUint16(header[sectStart+34:], 0)
	binary.LittleEndian.PutUint32(header[sectStart+36:], 0x60000020)

	// Minimal entry stub at 0x200, matching the COFF machine type.
	switch machineType {
	case 0xAA64:
		copy(header[0x200:], []byte{
			0x00, 0x00, 0x80, 0x52, // mov w0, #0
			0xC0, 0x03, 0x5F, 0xD6, // ret
		})
	default:
		header[0x200] = 0x31 // xor eax, eax
		header[0x201] = 0xC0
		header[0x202] = 0xC3 // ret
	}
}

// replaceAll is a simple string replacement helper
func replaceAll(s, old, new string) string {
	result := s
	for {
		i := indexOf(result, old)
		if i < 0 {
			break
		}
		result = result[:i] + new + result[i+len(old):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

//go:embed ape-m1.c.gz
var apeM1SourceGz []byte

// getApeLoaderSource returns the gzipped APE loader C source for macOS ARM64.
// The copy embedded in the toolchain (ape-m1.c.gz) is used unless the
// APE_LOADER_SOURCE environment variable points at an alternative ape-m1.c.
func getApeLoaderSource() []byte {
	if path := os.Getenv("APE_LOADER_SOURCE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			Exitf("APE_LOADER_SOURCE: %v", err)
		}
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(data); err != nil {
			Exitf("compressing APE loader source: %v", err)
		}
		if err := gz.Close(); err != nil {
			Exitf("compressing APE loader source: %v", err)
		}
		return buf.Bytes()
	}
	return apeM1SourceGz
}
