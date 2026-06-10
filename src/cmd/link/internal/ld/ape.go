// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
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
	apeHeaderSize = 65536

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

	// Verify it's a valid ELF
	if len(elfData) < 64 || string(elfData[0:4]) != elfMagic {
		Exitf("output file is not a valid ELF binary")
	}

	// Get ELF entry point and program headers for the embedded header
	elfEntry := binary.LittleEndian.Uint64(elfData[24:32])
	elfPhoff := binary.LittleEndian.Uint64(elfData[32:40])
	elfPhnum := binary.LittleEndian.Uint16(elfData[56:58])

	// Create the APE file
	apeFile, err := os.Create(outfile)
	if err != nil {
		Exitf("cannot create APE output: %v", err)
	}
	defer apeFile.Close()

	// Build the APE header with embedded formats
	header := makeAPEHeader(elfData, elfEntry, elfPhoff, elfPhnum, ctxt.Arch.Family)

	if _, err := apeFile.Write(header); err != nil {
		Exitf("cannot write APE header: %v", err)
	}

	// Adjust program header p_offset values to account for APE header
	// The ELF data will be at file offset apeHeaderSize, so all p_offset
	// values need to be increased by apeHeaderSize
	elfPhentsize := binary.LittleEndian.Uint16(elfData[54:56])
	for i := uint16(0); i < elfPhnum; i++ {
		phdrOffset := elfPhoff + uint64(i)*uint64(elfPhentsize)
		// p_offset is at byte 8 of each program header (64-bit ELF)
		pOffset := binary.LittleEndian.Uint64(elfData[phdrOffset+8:])
		binary.LittleEndian.PutUint64(elfData[phdrOffset+8:], pOffset+uint64(apeHeaderSize))
	}

	// Write the ELF payload at the expected offset
	if _, err := apeFile.Write(elfData); err != nil {
		Exitf("cannot write ELF payload: %v", err)
	}

	// Make executable
	if err := os.Chmod(outfile, 0755); err != nil {
		Exitf("cannot chmod APE output: %v", err)
	}
}

// makeAPEHeader creates an APE header following the specification.
// The header is a polyglot containing:
// - MZ/PE header for Windows
// - Shell script with printf-encoded ELF header for Linux/BSD
// - Mach-O header and dd command for macOS x86-64
func makeAPEHeader(elfData []byte, elfEntry, elfPhoff uint64, elfPhnum uint16, arch sys.ArchFamily) []byte {
	header := make([]byte, apeHeaderSize)

	// Determine page size based on architecture
	pageSize := uint64(pageSize4K)
	if arch == sys.ARM64 {
		pageSize = pageSize16K
	}

	// ELF payload starts after the APE header
	elfOffset := uint64(apeHeaderSize)

	// Calculate the actual entry point in the APE file
	// The ELF entry point is relative to the ELF load address
	// We need to adjust for the APE header offset
	apeEntry := elfEntry

	// Create the modified ELF header that points into the APE file
	// This header will be encoded as octal in a printf statement
	embeddedElf := makeEmbeddedElfHeader(elfData, elfOffset, pageSize, arch)

	// Create Mach-O header for macOS x86-64
	var machoHeader []byte
	var machoOffset, machoSize int
	if arch == sys.AMD64 {
		machoHeader = makeMachoHeader(elfData, elfOffset, apeEntry)
		// Place Mach-O header at a specific location in the APE header
		// It will be copied backward by the dd command
		machoOffset = 0x1000 // 4KB into the header
		machoSize = len(machoHeader)
	}

	// Load and compress APE loader source for macOS ARM64
	var apeLoaderGz []byte
	var apeLoaderOffset, apeLoaderSize int
	if arch == sys.ARM64 {
		apeLoaderGz = getApeLoaderSource()
		if len(apeLoaderGz) > 0 {
			// Place gzipped loader at offset 0x8000 (32KB into header)
			// This leaves room for script (0x400-0x8000) and avoids conflicts
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
	// - Script at 0x400 starts with "__APE__\n" to terminate heredoc

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

	// The PE header at 0x80 will be absorbed by the here-doc
	// We need to place the here-doc terminator and script after the PE header area

	// The script starts at offset 0x400 (after PE code section)
	// Build the script content
	var script bytes.Buffer

	// Here-doc terminator
	script.WriteString("__APE__\n")

	// APE execution logic - architecture-specific
	// For AMD64 builds: run on x86_64, require Rosetta on ARM64 macOS
	// For ARM64 builds: run on ARM64, use APE loader on macOS
	script.WriteString("m=$(uname -m 2>/dev/null) || m=x86_64\n")

	if arch == sys.AMD64 {
		// AMD64 binary: runs on x86_64 systems.
		//
		// Note for the ARM64-macOS (Rosetta) branch below: it cannot work on
		// current macOS. The assimilated Mach-O fails codesign's strict
		// validation (verified on macOS 15.7: "main executable failed strict
		// validation"), and Apple Silicon SIGKILLs unsigned executables even
		// under Rosetta. Native ARM64 macOS execution requires ARM64 code via
		// the compiled APE loader path (see the sys.ARM64 branch and the
		// No-Rosetta policy in CLAUDE.md); the branch is kept only to print
		// an actionable error.
		script.WriteString(`if [ "$m" = x86_64 ] || [ "$m" = amd64 ]; then
  o="$(command -v "$0")"
  exec 7<> "$o" || exit 121
  printf '`)
		for _, b := range embeddedElf {
			if b == '\'' {
				script.WriteString("'\\''")
			} else if b >= 0x20 && b < 0x7f && b != '\\' {
				script.WriteByte(b)
			} else {
				fmt.Fprintf(&script, "\\%03o", b)
			}
		}
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
		script.WriteString(`  exec "$0" "$@"
fi
if [ "$m" = aarch64 ] || [ "$m" = arm64 ]; then
  if [ -d /Applications ]; then
    echo 'APE: this amd64-only binary cannot run natively on ARM64 macOS.' >&2
    echo 'APE: rebuild with ARM64 (fat APE) support to run on Apple Silicon.' >&2
    exit 1
  fi
  echo 'APE: ARM64 Linux cannot run x86_64 binary' >&2
  exit 1
fi
`)
	} else if arch == sys.ARM64 {
		// ARM64 binary: runs on ARM64 systems
		// Must handle both Linux ARM64 and macOS ARM64 (Apple Silicon)
		// macOS cannot execute ELF directly - needs compiled APE loader
		script.WriteString(`if [ "$m" = x86_64 ] || [ "$m" = amd64 ]; then
  echo 'APE: x86_64 cannot run ARM64 binary' >&2
  exit 1
fi
o="$(command -v "$0")"
t="${TMPDIR:-${HOME:-.}}/.ape-1.10"
if [ "$m" = aarch64 ] || [ "$m" = arm64 ]; then
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
  else
    # Linux ARM64: check for loader, or transform ELF header
    type ape >/dev/null 2>&1 && exec ape "$o" "$@"
    [ -x "$t" ] && exec "$t" "$o" "$@"
    exec 7<> "$o" || exit 121
    printf '`)
		for _, b := range embeddedElf {
			if b == '\'' {
				script.WriteString("'\\''")
			} else if b >= 0x20 && b < 0x7f && b != '\\' {
				script.WriteByte(b)
			} else {
				fmt.Fprintf(&script, "\\%03o", b)
			}
		}
		script.WriteString("' >&7\n")
		script.WriteString(`    exec 7<&-
    exec "$0" "$@"
  fi
fi
`)
	}

	script.WriteString(`# Windows shells (MSYS/Cygwin): delegate to cmd.exe for PE execution
case "$(uname -s 2>/dev/null)" in
CYGWIN*|MINGW*|MSYS*) exec cmd //c "$0" "$@" ;;
esac
echo 'APE: unsupported platform' >&2
exit 1
`)

	scriptBytes := script.Bytes()

	// Replace APE loader offset/size placeholders for ARM64
	if arch == sys.ARM64 && apeLoaderSize > 0 {
		// Calculate actual file offset (APE header offset + offset within header)
		actualOffset := apeLoaderOffset
		scriptStr := string(scriptBytes)
		scriptStr = bytes.NewBuffer(nil).String() // Reset
		scriptStr = string(scriptBytes)
		scriptStr = replaceAll(scriptStr, "APE_LOADER_OFFSET", fmt.Sprintf("%d", actualOffset))
		scriptStr = replaceAll(scriptStr, "APE_LOADER_SIZE", fmt.Sprintf("%d", apeLoaderSize))
		scriptBytes = []byte(scriptStr)
	}

	// Place script at offset 0x400
	scriptOffset := 0x400
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
	copy(header[scriptOffset:], scriptBytes)

	// Embed gzipped APE loader source for ARM64
	if arch == sys.ARM64 && apeLoaderSize > 0 {
		if apeLoaderOffset+apeLoaderSize > apeHeaderSize {
			Exitf("APE loader too large to embed: %d bytes at offset %d", apeLoaderSize, apeLoaderOffset)
		}
		copy(header[apeLoaderOffset:], apeLoaderGz)
	}

	// === PE Header at offset 0x80 ===
	// Required for Windows support
	writePEHeader(header, arch)

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

	// Minimal code at 0x200
	header[0x200] = 0x31 // xor eax, eax
	header[0x201] = 0xC0
	header[0x202] = 0xC3 // ret
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

// getApeLoaderSource returns the gzipped APE loader C source for macOS ARM64.
// The source is loaded from the cosmopolitan source tree if available.
func getApeLoaderSource() []byte {
	// Try to find ape-m1.c in common locations
	paths := []string{
		// Relative to GOROOT (this repo)
		filepath.Join(os.Getenv("GOROOT"), "..", "cosmopolitan", "ape", "ape-m1.c"),
		// User's repos directory
		filepath.Join(os.Getenv("HOME"), "repos", "cosmopolitan", "ape", "ape-m1.c"),
		// Environment variable override
		os.Getenv("APE_LOADER_SOURCE"),
	}

	var sourceData []byte
	for _, path := range paths {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			sourceData = data
			break
		}
	}

	if len(sourceData) == 0 {
		// No source found - ARM64 macOS support will be limited
		return nil
	}

	// Gzip compress the source
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(sourceData); err != nil {
		return nil
	}
	if err := gz.Close(); err != nil {
		return nil
	}

	return buf.Bytes()
}
