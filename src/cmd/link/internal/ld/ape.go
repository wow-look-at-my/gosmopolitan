// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/cosmoape"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// APE (Actually Portable Executable) format implementation
// Based on the specification at: ape/specification.md
//
// APE creates polyglot executables that work on multiple OSes:
// - Linux: Uses embedded ELF header (encoded as octal in printf)
// - macOS x86-64: Uses dd command to copy Mach-O header backward
// - macOS ARM64: Runs the arm64 payload natively via the embedded APE
//   loader source (compiled with cc on first run); no Rosetta involved
// - Windows: A real PE header maps the embedded cosmo amd64 image and
//   enters the runtime's NT boot stub through loader-resolved kernel32
//   imports; the stub joins the common runtime boot with the host
//   marked as NT (rt0_cosmo_nt_amd64.s). arm64-only APEs keep a
//   parseable do-nothing stub PE header instead.
// - Windows shell (MSYS/Cygwin): Delegates to cmd.exe for PE execution

const (
	// APE header must be page-aligned for ELF loading
	// Using 64KB for Windows allocation granularity compatibility
	apeHeaderSize   = 65536
	apeScriptOffset = 0x800

	// What follows the script, and so how much room the script has: it runs
	// from apeScriptOffset up to the Mach-O header, and the gzipped macOS
	// ARM64 loader source sits after that.
	apeMachoOffset     = 0x2000
	apeLoaderSrcOffset = 0x8000

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

	// x86_THREAD_STATE64 register file: rax rbx rcx rdx rdi rsi rbp rsp
	// r8-r15 rip rflags cs fs gs. XNU's load_threadstack validates that
	// (count+2)*4 bytes exactly consume cmdsize-8, so the emitted state
	// must be exactly this many quadwords - no more, no less.
	machoThreadStateRegs   = 21
	machoThreadStateFlavor = 4                           // x86_THREAD_STATE64
	machoUnixThreadCmdSize = 16 + machoThreadStateRegs*8 // cmd+cmdsize+flavor+count + registers = 184

	// machoHostXNU is the host-OS indicator handed to the entry point in
	// CL when the kernel loads the dd-assimilated Mach-O directly. It
	// must match _HOSTXNU in runtime/os_cosmo_amd64.go; rt0_cosmo_amd64.s
	// reads it to route syscalls through Apple's ABI instead of issuing
	// raw Linux syscalls (which die with SIGSYS on macOS).
	machoHostXNU = 8

	// Segment protection
	machoProtRead  = 0x1
	machoProtWrite = 0x2
	machoProtExec  = 0x4

	// machoSegmentCmdSize is the size of an LC_SEGMENT_64 with no sections.
	machoSegmentCmdSize = 72

	// machoPageSize is the page size of x86-64 XNU. The kernel's
	// load_segment rejects (LOAD_BADMACHO) any segment whose file offset
	// or vm address is not page-aligned, and maps vm ranges in whole
	// pages, so segment vm sizes are rounded up to this.
	machoPageSize = 0x1000
)

// convertToAPE converts an ELF binary to Actually Portable Executable format.
// apePayload describes one architecture's ELF image embedded in an APE file.
type apePayload struct {
	elf    []byte // complete ELF image; p_offset values are payload-relative
	arch   sys.ArchFamily
	offset uint64 // file offset of this image inside the APE; set by layoutAPE

	// pe carries the symbol RVAs the real amd64 PE header needs. It is
	// set only on the thin-link path (convertToAPE), where the loader is
	// alive to resolve them; nil means no real PE header can be computed
	// for this payload.
	pe *apePEInfo

	// head is the 64K APE head of the input file this payload was
	// extracted from, set only on the -apefat merge path. The amd64
	// input's head already contains the real PE header computed by its
	// thin link, valid verbatim in the fat file (the amd64 image lands
	// at the same file offset with identical bytes), so the fat header
	// transplants it instead of recomputing.
	head []byte
}

// apePEInfo holds the image RVAs, resolved from the live link's symbol
// table, that writePECosmoAMD64 places in the PE header. RVAs are
// relative to peCosmoImageBase, and for this layout equal payload-
// relative file offsets (every PT_LOAD has vaddr - p_offset ==
// peCosmoImageBase; see apePayloadLoads).
type apePEInfo struct {
	entryRVA   uint32 // _rt0_cosmo_nt, the PE AddressOfEntryPoint
	importsRVA uint32 // runtime.ntidata, the import directory table
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
	// Validate the program header table up front: shiftPOffsets,
	// makeEmbeddedElfHeader, makeMachoHeader, payloadExtent,
	// stripPayload, and apePayloadLoads all index it without further
	// checks, so a truncated or corrupt input would panic there.
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phentsize := binary.LittleEndian.Uint16(elf[54:56])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	if phentsize != 56 {
		return nil, fmt.Errorf("corrupt ELF: e_phentsize is %d, want 56", phentsize)
	}
	if phnum == 0 {
		return nil, fmt.Errorf("corrupt ELF: no program headers")
	}
	if phoff > uint64(len(elf)) || uint64(phnum)*56 > uint64(len(elf))-phoff {
		return nil, fmt.Errorf("corrupt ELF: program header table (e_phoff %#x, e_phnum %d) extends past end of file (%d bytes)", phoff, phnum, len(elf))
	}
	return &apePayload{elf: elf, arch: arch}, nil
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
	if p.arch == sys.AMD64 {
		apePrepareNTBoot(ctxt, p)
	}
	writeAPEFile(outfile, []*apePayload{p})
}

// apePayloadAlign is the alignment of payload images within the APE file.
// The APE loader requires p_vaddr to be congruent to p_offset modulo 16384
// for every program header; placing payloads on 64K boundaries (the largest
// page size in play) preserves whatever congruence each image already has.
const apePayloadAlign = 0x10000

// layoutAPE assigns file offsets to the payloads: the first begins right
// after the APE header, each subsequent payload at the next aligned boundary.
func layoutAPE(payloads []*apePayload) {
	off := uint64(apeHeaderSize)
	for _, p := range payloads {
		p.offset = off
		off += uint64(len(p.elf))
		off = (off + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1)
	}
}

// writeAPEFile writes an APE polyglot containing the given payloads.
// Payload p_offset values are rewritten to absolute file offsets.
func writeAPEFile(outfile string, payloads []*apePayload) {
	layoutAPE(payloads)
	header := makeAPEHeaderForPayloads(payloads)

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
	if end := apePEFileEnd(payloads); end > cur {
		if _, err := apeFile.Write(make([]byte, end-cur)); err != nil {
			Exitf("cannot write APE PE padding: %v", err)
		}
	}

	if err := os.Chmod(outfile, 0755); err != nil {
		Exitf("cannot chmod APE output: %v", err)
	}
}

// apePEFileEnd returns the file offset the amd64 payload's PE sections
// reach, or 0 when there is no amd64 payload. .data's SizeOfRawData is
// p_filesz rounded up to FileAlignment (writePECosmoAMD64), so the PE image
// extends past the payload's loadable span by up to FileAlignment-1 bytes
// of zero padding.
//
// Whether the file covers that tail used to be luck: an unstripped payload
// carries its debug tail past it, and in a fat APE the next payload's
// alignment padding covers it. A STRIPPED amd64 payload with nothing after
// it ends exactly at its loadable span, leaving the PE header referencing
// bytes past EOF - which the NT loader rejects outright ("%1 is not a valid
// Win32 application"), the whole image, not just that section.
func apePEFileEnd(payloads []*apePayload) uint64 {
	for _, p := range payloads {
		if p.arch != sys.AMD64 {
			continue
		}
		loads := apePayloadLoads(p.elf)
		if len(loads) == 0 {
			return 0
		}
		data := loads[len(loads)-1]
		rounded := (data.filesz + peCosmoFileAlign - 1) &^ uint64(peCosmoFileAlign-1)
		return p.offset + data.off + rounded
	}
	return 0
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
// format string as a conversion directive, corrupting the header
// write whenever a variable header byte (e_entry, e_phoff, ...) is 0x25.
func writePrintfBlob(script *bytes.Buffer, blob []byte) {
	script.WriteString(printfBlob(blob))
}

// printfBlob is writePrintfBlob's escaping, for a caller that renders the
// statement from a template instead of writing it piece by piece.
func printfBlob(blob []byte) string {
	var b strings.Builder
	for _, c := range blob {
		if c >= 0x20 && c < 0x7f && c != '\\' && c != '\'' && c != '%' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	return b.String()
}

// apeRunDir is where the bootstrap script stages the runnable copy it makes
// of itself. The trailing number is the staging layout's version: a binary
// built by an older linker keeps its own directory rather than reading one
// written to a different shape.
//
// TMPDIR first, then HOME, matching the loader path the macOS ARM64 branch
// already uses. Both are per-user, which is what keeps another user from
// planting a binary at the path this script is about to exec. /tmp is the last
// resort, for a host that sets neither.
const apeRunDir = `${TMPDIR:-${HOME:-/tmp}}/.ape-run-1`

// writeStagedCopy emits the shell that gives the host a runnable copy of the
// APE at "$p", and never touches the APE itself.
//
// The kernel cannot exec this file: it starts with the DOS/shell magic, not
// with an ELF or Mach-O header. Something has to write the real header over
// those first bytes, and for years that something wrote them into the file it
// was running -- which needs the file to be writable, breaks its checksum,
// and costs a fat APE every platform but this one. So the script copies
// itself once and corrects the COPY.
//
// The copy is keyed by the identity of the file it came from (device, inode,
// mtime and size, or a checksum where stat is missing), so a rebuilt binary
// stages a new copy and a re-run of the same one costs a stat and an exec.
//
// argv[0] becomes the copy's path. The copy keeps the original's basename to
// hold `${0##*/}` steady, which is what a usage line prints.
func writeStagedCopy(script *bytes.Buffer, boot []byte, machoOffset, machoSize int) {
	const ddBlockSize = 8
	data := struct {
		RunDir    string
		Boot      string
		Macho     bool
		BlockSize int
		Skip      int
		Count     int
	}{
		RunDir:    apeRunDir,
		Boot:      printfBlob(boot),
		Macho:     machoSize > 0,
		BlockSize: ddBlockSize,
		Skip:      machoOffset / ddBlockSize,
		Count:     (machoSize + ddBlockSize - 1) / ddBlockSize,
	}
	if err := apeStageTmpl.Execute(script, data); err != nil {
		Exitf("APE: rendering the staging script: %v", err)
	}
}

// apeStageTmpl is the shell writeStagedCopy renders. The Mach-O block runs
// after the ELF one because a host that carries both is macOS, where the
// Mach-O header is the one that counts.
var apeStageTmpl = template.Must(template.New("apestage").Parse(
	`  k=$(stat -c %d.%i.%Y.%s "$o" 2>/dev/null || stat -f %d.%i.%m.%z "$o" 2>/dev/null || cksum <"$o" | tr -d ' ')
  c="{{.RunDir}}/$k"
  p="$c/${0##*/}"
  if [ ! -x "$p" ]; then
    (umask 077; mkdir -p "$c") || { echo "APE: cannot create $c" >&2; exit 121; }
    cp "$o" "$p.$$" || { echo "APE: cannot stage $p" >&2; exit 121; }
{{- if .Boot}}
    exec 7<> "$p.$$" || { echo "APE: cannot stage $p" >&2; exit 121; }
    printf '{{.Boot}}' >&7
    exec 7<&-
{{- end}}
{{- if .Macho}}
    if [ -d /Applications ]; then
      dd if="$p.$$" of="$p.$$" bs={{.BlockSize}} skip={{.Skip}} count={{.Count}} conv=notrunc 2>/dev/null || { echo 'APE: Mach-O relocation failed' >&2; exit 121; }
    fi
{{- end}}
    chmod 755 "$p.$$" && mv -f "$p.$$" "$p" || { rm -f "$p.$$"; echo "APE: cannot stage $p" >&2; exit 121; }
  fi
  exec "$p" "$@"
`))

// makeAPEHeaderForPayloads creates the 64K APE polyglot header that boots
// the given payloads (at most one per architecture family). With both an
// amd64 and an arm64 payload the result is a fat APE: the bootstrap script
// and the embedded boot headers dispatch on the host architecture, and the
// macOS ARM64 APE loader finds the aarch64 image by decoding every printf
// statement in the first 8192 bytes.
//
// Which boot mechanisms the header carries follows apePlatforms: each
// selected platform contributes exactly the pieces it boots through, and a
// host outside the selection gets a message naming what the binary was
// built for. The header is a fixed 64K region either way, so deselecting a
// platform without also dropping its payload architecture changes what the
// binary claims, not what it weighs.
func makeAPEHeaderForPayloads(payloads []*apePayload) []byte {
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

	plat := apePlatforms(payloads)
	linuxAMD := plat.Has(cosmoape.LinuxAMD64)
	darwinAMD := plat.Has(cosmoape.DarwinAMD64)
	windowsAMD := plat.Has(cosmoape.WindowsAMD64)
	linuxARM := plat.Has(cosmoape.LinuxARM64)
	darwinARM := plat.Has(cosmoape.DarwinARM64)

	header := make([]byte, apeHeaderSize)

	// Embedded (printf-encoded) boot ELF headers. They serve two purposes:
	// self-assimilation on Linux, and discovery by the macOS ARM64 APE
	// loader, which octal-decodes every printf in the first 8192 bytes and
	// uses the first one with an aarch64 machine type.
	var amdBoot, armBoot []byte
	if linuxAMD {
		amdBoot = makeEmbeddedElfHeader(amd.elf, amd.offset, sys.AMD64)
	}
	if linuxARM || darwinARM {
		armBoot = makeEmbeddedElfHeader(arm.elf, arm.offset, sys.ARM64)
	}

	// Create Mach-O header for macOS x86-64
	var machoHeader []byte
	var machoOffset, machoSize int
	if darwinAMD {
		machoHeader = makeMachoHeader(amd.elf, amd.offset, amd.entry())
		// Place Mach-O header at a specific location in the APE header.
		// It will be copied backward by the dd command.
		//
		// 8KB in, not 4KB: the bootstrap script runs from 0x800 up to here,
		// and it grew when it stopped writing headers into the file it runs.
		machoOffset = apeMachoOffset
		machoSize = len(machoHeader)
	}

	// Load gzipped APE loader source for macOS ARM64
	var apeLoaderGz []byte
	var apeLoaderOffset, apeLoaderSize int
	if darwinARM {
		apeLoaderGz = getApeLoaderSource()
		if len(apeLoaderGz) > 0 {
			// Place gzipped loader at offset 0x8000 (32KB into header).
			// This leaves room for the script.
			apeLoaderOffset = apeLoaderSrcOffset
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
	switch {
	case linuxAMD || darwinAMD:
		script.WriteString("  o=\"$(command -v \"$0\")\"\n")
		if !linuxAMD {
			// Without a boot ELF header there is nothing to assimilate
			// into, and re-execing would spin on this script forever.
			fmt.Fprintf(&script, "  [ -d /Applications ] || { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		if !darwinAMD {
			// Refuse macOS before staging a copy: the printf writes an ELF
			// header, and with no Mach-O header to put over it the copy
			// would not run there anyway.
			fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		writeStagedCopy(&script, amdBoot, machoOffset, machoSize)
	case amd == nil:
		script.WriteString("  echo 'APE: x86_64 cannot run ARM64 binary' >&2\n")
		script.WriteString("  exit 1\n")
	default:
		fmt.Fprintf(&script, "  %s\n  exit 1\n", apeUnsupportedEcho(plat))
	}
	script.WriteString("fi\n")

	// --- ARM64 hosts ---
	script.WriteString("if [ \"$m\" = aarch64 ] || [ \"$m\" = arm64 ]; then\n")
	if arm != nil {
		script.WriteString("  o=\"$(command -v \"$0\")\"\n")
		script.WriteString("  t=\"${TMPDIR:-${HOME:-.}}/.ape-1.10\"\n")
		if darwinARM {
			script.WriteString(`  if [ -d /Applications ]; then
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
`)
		}
		if linuxARM && !darwinARM {
			// Same trap as the amd64 branch: an ELF header on a macOS host
			// leaves something that runs nowhere.
			fmt.Fprintf(&script, "  [ -d /Applications ] && { %s; exit 1; }\n", apeUnsupportedEcho(plat))
		}
		if linuxARM {
			script.WriteString(`  # Linux ARM64: an installed loader runs the file as it stands
  type ape >/dev/null 2>&1 && exec ape "$o" "$@"
  [ -x "$t" ] && exec "$t" "$o" "$@"
`)
			writeStagedCopy(&script, armBoot, 0, 0)
		} else {
			// The printf below is unreachable shell on purpose: the macOS
			// APE loader locates the aarch64 boot header by decoding every
			// printf in the file's first 8192 bytes, and it is the only
			// reader of this one once Linux ARM64 is deselected.
			fmt.Fprintf(&script, "  %s\n  exit 1\n", apeUnsupportedEcho(plat))
			script.WriteString("  printf '")
			writePrintfBlob(&script, armBoot)
			script.WriteString("' >&7\n")
		}
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

	if windowsAMD {
		script.WriteString(`# Windows shells (MSYS/Cygwin): delegate to cmd.exe for PE execution
case "$(uname -s 2>/dev/null)" in
CYGWIN*|MINGW*|MSYS*) exec cmd //c "$0" "$@" ;;
esac
`)
	}
	script.WriteString(`echo 'APE: unsupported platform' >&2
exit 1
`)

	scriptBytes := script.Bytes()

	// Replace APE loader offset/size placeholders for the macOS ARM64 path
	if arm != nil && apeLoaderSize > 0 {
		s := string(scriptBytes)
		s = strings.ReplaceAll(s, "APE_LOADER_OFFSET", fmt.Sprintf("%d", apeLoaderOffset))
		s = strings.ReplaceAll(s, "APE_LOADER_SIZE", fmt.Sprintf("%d", apeLoaderSize))
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
	// The polyglot's MZ magic and e_lfanew presume a PE image header
	// here. For windows/amd64 the header really maps the embedded cosmo
	// image and enters the runtime's NT boot stub: computed from the live
	// link's symbols on the thin path, transplanted verbatim from the
	// amd64 input's head on the fat path (same payload offset, same
	// bytes, so the thin header is valid as-is). Otherwise the legacy
	// do-nothing stub keeps the file parseable as a PE.
	switch {
	case !windowsAMD:
		stubArch := sys.ARM64
		if amd != nil {
			stubArch = sys.AMD64
		}
		writePEHeader(header, stubArch)
	case amd.pe != nil:
		writePECosmoAMD64(header, amd)
	case amd.head != nil:
		if *flagApePlatforms != "" {
			checkNTBootHead(amd)
		}
		transplantPEHeader(header, amd)
	case *flagApePlatforms != "":
		Exitf("-apeplatforms selects %s, but the amd64 input carries no NT boot header: pass the thin APE this linker produced, not a raw ELF", cosmoape.WindowsAMD64)
	default:
		writePEHeader(header, sys.AMD64)
	}

	// === Mach-O header for macOS x86-64 ===
	// The header region runs from machoOffset up to the APE loader source
	// (or the end of the APE header); growing past it would silently
	// clobber the loader, so fail loudly instead.
	if machoSize > 0 {
		if machoOffset+machoSize > apeHeaderSize {
			Exitf("APE Mach-O header (%d bytes at %#x) exceeds the %d-byte APE header", machoSize, machoOffset, apeHeaderSize)
		}
		if apeLoaderSize > 0 && machoOffset+machoSize > apeLoaderOffset {
			Exitf("APE Mach-O header (%d bytes at %#x) overlaps the APE loader at %#x", machoSize, machoOffset, apeLoaderOffset)
		}
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
func makeEmbeddedElfHeader(origElf []byte, elfOffset uint64, arch sys.ArchFamily) []byte {
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

	// Section header fields normally stay zero: execution never reads
	// them, and a pristine payload's own table sits at a payload-relative
	// offset that would be wrong in the assimilated file. The one producer
	// of an exception is the -apefat compact debug mode (apedebug.go),
	// whose payload ehdrs reference a section-header view appended past
	// the payload image at an ABSOLUTE APE file offset - recognizable
	// here as an offset at or beyond the payload image's end. That offset
	// stays correct after self-assimilation rewrites the file's first 64
	// bytes with this header, so propagating it is exactly what lets
	// debuggers find the appended debug info in the assimilated binary.
	// Every other payload shape (thin links, stripped or full fat merges)
	// keeps today's zeroed fields, bit for bit.
	if shoff := binary.LittleEndian.Uint64(origElf[40:48]); shoff >= uint64(len(origElf)) {
		binary.LittleEndian.PutUint64(hdr[40:], shoff)
		copy(hdr[60:64], origElf[60:64]) // e_shnum, e_shstrndx
	}

	return hdr
}

// machoSegment describes one LC_SEGMENT_64 load command.
type machoSegment struct {
	name     string
	vmaddr   uint64
	vmsize   uint64
	fileoff  uint64 // absolute offset in the APE file
	filesize uint64
	prot     uint32 // used for both initprot and maxprot
}

// machoSegmentsFromELF derives the Mach-O segment table from the payload's
// PT_LOAD program headers. elfData's p_offset values are still
// payload-relative when this runs (shiftPOffsets rewrites them to absolute
// file offsets at write-out), so file offsets are computed as
// elfOffset + p_offset, matching the bytes of the final APE file.
//
// The first (executable) load is extended downward to file offset 0 so that
// it also maps the APE polyglot header - and, after the dd transform, the
// Mach-O header itself. XNU's parse_machfile rejects (LOAD_BADMACHO) any
// executable in which no R+X segment maps the start of the file
// (found_header_segment); extending the text segment mirrors what
// Cosmopolitan's ape.S Mach-O header does.
func machoSegmentsFromELF(elfData []byte, elfOffset uint64) []machoSegment {
	elfPhoff := binary.LittleEndian.Uint64(elfData[32:40])
	elfPhentsize := binary.LittleEndian.Uint16(elfData[54:56])
	elfPhnum := binary.LittleEndian.Uint16(elfData[56:58])

	var segs []machoSegment
	for i := uint16(0); i < elfPhnum; i++ {
		phdr := elfData[elfPhoff+uint64(i)*uint64(elfPhentsize):]
		if binary.LittleEndian.Uint32(phdr[0:4]) != 1 { // PT_LOAD
			continue
		}
		flags := binary.LittleEndian.Uint32(phdr[4:8])
		off := binary.LittleEndian.Uint64(phdr[8:16])
		vaddr := binary.LittleEndian.Uint64(phdr[16:24])
		filesz := binary.LittleEndian.Uint64(phdr[32:40])
		memsz := binary.LittleEndian.Uint64(phdr[40:48])
		if memsz < filesz {
			Exitf("APE Mach-O: PT_LOAD %d has p_memsz %#x < p_filesz %#x", i, memsz, filesz)
		}
		var prot uint32
		if flags&0x4 != 0 { // PF_R
			prot |= machoProtRead
		}
		if flags&0x2 != 0 { // PF_W
			prot |= machoProtWrite
		}
		if flags&0x1 != 0 { // PF_X
			prot |= machoProtExec
		}
		// Segment names are advisory; pick conventional ones per class.
		name := "__RODATA"
		switch {
		case prot&machoProtExec != 0:
			name = "__TEXT"
		case prot&machoProtWrite != 0:
			name = "__DATA"
		}
		segs = append(segs, machoSegment{
			name:   name,
			vmaddr: vaddr,
			// Rounding p_memsz up to whole pages preserves the BSS:
			// XNU zero-fills [filesize, vmsize) of a segment, so a
			// data segment with p_memsz > p_filesz gets its zero
			// pages from the kernel, like ELF.
			vmsize:   (memsz + machoPageSize - 1) &^ (machoPageSize - 1),
			fileoff:  elfOffset + off,
			filesize: filesz,
			prot:     prot,
		})
	}
	if len(segs) == 0 {
		Exitf("APE Mach-O: ELF payload has no PT_LOAD segments")
	}

	// Extend the first load down to file offset 0 (see function comment).
	first := &segs[0]
	if first.prot&(machoProtRead|machoProtExec) != machoProtRead|machoProtExec {
		Exitf("APE Mach-O: first PT_LOAD is not readable+executable (prot %#x); cannot map the file header", first.prot)
	}
	ext := first.fileoff
	if first.vmaddr <= ext {
		Exitf("APE Mach-O: first PT_LOAD vmaddr %#x is too low to extend the segment to file offset 0", first.vmaddr)
	}
	first.fileoff = 0
	first.filesize += ext
	first.vmaddr -= ext
	first.vmsize += ext

	// XNU's load_segment refuses to map anything whose file offset or vm
	// address is not page-aligned, and overlapping vm ranges would fail
	// vm_map_enter at load time. Go's ELF layout guarantees both (loads
	// are page-rounded and laid out consecutively), so a violation means
	// the layout changed and this code needs revisiting.
	for i := range segs {
		s := &segs[i]
		if s.fileoff%machoPageSize != 0 || s.vmaddr%machoPageSize != 0 {
			Exitf("APE Mach-O: segment %s (fileoff %#x, vmaddr %#x) is not page-aligned", s.name, s.fileoff, s.vmaddr)
		}
		if s.filesize > s.vmsize {
			Exitf("APE Mach-O: segment %s filesize %#x exceeds vmsize %#x", s.name, s.filesize, s.vmsize)
		}
		if i > 0 {
			prev := &segs[i-1]
			if s.vmaddr < prev.vmaddr+prev.vmsize {
				Exitf("APE Mach-O: segment %s (vmaddr %#x) overlaps %s (ends %#x) after page rounding", s.name, s.vmaddr, prev.name, prev.vmaddr+prev.vmsize)
			}
		}
	}
	return segs
}

// makeMachoHeader creates the Mach-O executable header for macOS x86-64.
//
// On x86-64 macOS the APE bootstrap script dd-copies this header (placed at
// offset 0x2000 in the APE header) over the start of a COPY of the file,
// turning that copy into a Mach-O executable whose load commands point
// straight at the embedded amd64 ELF image. The copy is what runs, and the
// APE itself is left as it was. The XNU kernel loads it directly - there is
// no dyld involved (LC_UNIXTHREAD, not LC_MAIN) - so the load commands must
// satisfy the kernel's parse_machfile/load_segment checks on their own:
//
//   - __PAGEZERO covers [0, lowest mapped address) with no access.
//   - One LC_SEGMENT_64 per PT_LOAD, with initprot/maxprot translated from
//     p_flags and vmsize covering p_memsz (BSS zero-fill), at the ELF's own
//     virtual addresses.
//   - The text segment is extended down to file offset 0 so an R+X segment
//     maps the Mach-O header (a hard kernel requirement).
//   - LC_UNIXTHREAD holds rip = the ELF entry point (unmodified) and the
//     XNU host-OS indicator in rcx for rt0_cosmo_amd64.s.
func makeMachoHeader(elfData []byte, elfOffset uint64, elfEntry uint64) []byte {
	segs := machoSegmentsFromELF(elfData, elfOffset)

	// XNU only accepts an entry point that falls inside a segment mapped
	// readable+executable (parse_machfile's validentry check).
	validEntry := false
	for _, s := range segs {
		if elfEntry >= s.vmaddr && elfEntry < s.vmaddr+s.vmsize &&
			s.prot&(machoProtRead|machoProtExec) == machoProtRead|machoProtExec {
			validEntry = true
		}
	}
	if !validEntry {
		Exitf("APE Mach-O: entry point %#x is not inside a readable+executable segment", elfEntry)
	}

	ncmds := 1 + len(segs) + 1 // __PAGEZERO + loads + LC_UNIXTHREAD
	sizeofcmds := machoSegmentCmdSize*(1+len(segs)) + machoUnixThreadCmdSize

	var buf bytes.Buffer

	// Mach-O header (32 bytes)
	binary.Write(&buf, binary.LittleEndian, uint32(machoMagic64))       // magic
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUTypeX64))    // cputype
	binary.Write(&buf, binary.LittleEndian, uint32(machoCPUSubtypeX64)) // cpusubtype
	binary.Write(&buf, binary.LittleEndian, uint32(machoFileTypeExec))  // filetype
	binary.Write(&buf, binary.LittleEndian, uint32(ncmds))              // ncmds
	binary.Write(&buf, binary.LittleEndian, uint32(sizeofcmds))         // sizeofcmds
	binary.Write(&buf, binary.LittleEndian, uint32(machoFlagNoUndefs))  // flags
	binary.Write(&buf, binary.LittleEndian, uint32(0))                  // reserved

	// __PAGEZERO: MH_EXECUTE images must not map anything below the page
	// zero region; XNU raises the vm map's minimum offset to its end.
	// vmaddr 0, filesize 0, prot 0/0 is the shape load_segment treats as
	// page zero.
	writeMachoSegment(&buf, machoSegment{name: "__PAGEZERO", vmsize: segs[0].vmaddr})

	for _, s := range segs {
		writeMachoSegment(&buf, s)
	}

	writeMachoUnixThread(&buf, elfEntry)

	if buf.Len() != 32+sizeofcmds {
		Exitf("APE Mach-O: internal error: wrote %d header bytes, want %d", buf.Len(), 32+sizeofcmds)
	}
	// The bootstrap script dd-copies the header in 8-byte blocks and
	// derives the block count from this buffer's length; pad to a block
	// boundary so the copy covers exactly the emitted header.
	for buf.Len()%8 != 0 {
		buf.WriteByte(0)
	}

	return buf.Bytes()
}

// writeMachoSegment emits one LC_SEGMENT_64 load command (no sections).
func writeMachoSegment(buf *bytes.Buffer, s machoSegment) {
	binary.Write(buf, binary.LittleEndian, uint32(machoLCSegment64))    // cmd
	binary.Write(buf, binary.LittleEndian, uint32(machoSegmentCmdSize)) // cmdsize
	var name [16]byte
	copy(name[:], s.name)
	buf.Write(name[:]) // segname
	binary.Write(buf, binary.LittleEndian, s.vmaddr)
	binary.Write(buf, binary.LittleEndian, s.vmsize)
	binary.Write(buf, binary.LittleEndian, s.fileoff)
	binary.Write(buf, binary.LittleEndian, s.filesize)
	binary.Write(buf, binary.LittleEndian, s.prot)    // maxprot
	binary.Write(buf, binary.LittleEndian, s.prot)    // initprot
	binary.Write(buf, binary.LittleEndian, uint32(0)) // nsects
	binary.Write(buf, binary.LittleEndian, uint32(0)) // flags
}

// writeMachoUnixThread emits the LC_UNIXTHREAD load command that starts the
// kernel-loaded Mach-O at entry. The register file is exactly the 21
// quadwords of x86_THREAD_STATE64 (count is expressed in 32-bit words, so
// 42), matching the declared cmdsize byte for byte. rsp is left zero, which
// makes XNU allocate a default stack; rcx carries the host-OS indicator so
// rt0_cosmo_amd64.s (which reads CL) knows it is running on XNU.
func writeMachoUnixThread(buf *bytes.Buffer, entry uint64) {
	binary.Write(buf, binary.LittleEndian, uint32(machoLCUnixThread))      // cmd
	binary.Write(buf, binary.LittleEndian, uint32(machoUnixThreadCmdSize)) // cmdsize
	binary.Write(buf, binary.LittleEndian, uint32(machoThreadStateFlavor)) // flavor (x86_THREAD_STATE64)
	binary.Write(buf, binary.LittleEndian, uint32(machoThreadStateRegs*2)) // count (32-bit words)

	var regs [machoThreadStateRegs]uint64
	regs[2] = machoHostXNU // rcx: host OS for rt0 (CL = 8 means XNU)
	regs[16] = entry       // rip
	for _, r := range regs {
		binary.Write(buf, binary.LittleEndian, r)
	}
}

// Real PE header parameters for the cosmo amd64 image (writePECosmoAMD64).
const (
	// peCosmoImageBase is the cosmo/amd64 link base (amd64/obj.go sets
	// FlagTextAddr = 0x100000000 + HEADR), already the multiple of 64K
	// the Windows loader demands of ImageBase. Every PT_LOAD of the
	// image satisfies vaddr - p_offset == peCosmoImageBase (the layout
	// invariant Vaddr == Fileoff mod FlagRound plus lockstep address/
	// offset assignment), so RVA == payload-relative file offset
	// throughout, and PointerToRawData == apeHeaderSize + RVA once the
	// payload sits at apeHeaderSize.
	peCosmoImageBase = 0x100000000
	peCosmoSectAlign = 0x1000
	peCosmoFileAlign = 0x200
	// peCosmoHeadersSize covers the real header chain (ends at 0x208)
	// rounded to FileAlignment; it must stay at or below the first
	// section RVA (0x1000) and at or below AddressOfEntryPoint.
	peCosmoHeadersSize = 0x400
	// peCosmoImportsSize is DataDirectory[1].Size: one import
	// descriptor plus the all-zero terminator entry.
	peCosmoImportsSize = 0x28
	// peCosmoSections is the section count of the real header (.text,
	// .rodata, .data), which also tells it apart from the 1-section stub.
	peCosmoSections = 3
)

// Fixed layout of the runtime.ntidata import blob. Must match the DATA
// directives and layout comment in runtime/rt0_cosmo_nt_amd64.s.
const (
	ntidataSize        = 0x70
	ntidataILT         = 0x28 // import lookup table (2 entries + terminator)
	ntidataHintGetProc = 0x40 // hint/name entry for GetProcAddress
	ntidataHintLoadLib = 0x52 // hint/name entry for LoadLibraryA
	ntidataDLLName     = 0x62 // "kernel32.dll\0"
	ntiatSize          = 24   // import address table (2 slots + terminator)
)

// apePhdr is one PT_LOAD program header of a payload image, with the
// payload-relative file offset.
type apePhdr struct {
	flags  uint32
	off    uint64
	vaddr  uint64
	filesz uint64
	memsz  uint64
}

// apePayloadLoads returns the PT_LOAD program headers of a payload whose
// table payloadFromELF has already validated.
func apePayloadLoads(elf []byte) []apePhdr {
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phentsize := binary.LittleEndian.Uint16(elf[54:56])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	var loads []apePhdr
	for i := uint16(0); i < phnum; i++ {
		ph := elf[phoff+uint64(i)*uint64(phentsize):]
		if binary.LittleEndian.Uint32(ph[0:4]) != 1 { // PT_LOAD
			continue
		}
		loads = append(loads, apePhdr{
			flags:  binary.LittleEndian.Uint32(ph[4:8]),
			off:    binary.LittleEndian.Uint64(ph[8:16]),
			vaddr:  binary.LittleEndian.Uint64(ph[16:24]),
			filesz: binary.LittleEndian.Uint64(ph[32:40]),
			memsz:  binary.LittleEndian.Uint64(ph[40:48]),
		})
	}
	return loads
}

// apeVaddrFileOff translates the virtual address range [vaddr,
// vaddr+size) to its payload-relative file offset, requiring the whole
// range to be file-backed (within p_filesz) by a single PT_LOAD.
func apeVaddrFileOff(loads []apePhdr, vaddr, size uint64, what string) uint64 {
	for _, l := range loads {
		if vaddr >= l.vaddr && vaddr+size <= l.vaddr+l.filesz {
			return l.off + (vaddr - l.vaddr)
		}
	}
	Exitf("APE NT boot: %s (vaddr %#x, %d bytes) is not file-backed by any PT_LOAD; it must be initialized data, not BSS", what, vaddr, size)
	return 0
}

// apePrepareNTBoot resolves the NT boot symbols from the live link,
// patches the five RVA fields of the runtime.ntidata import blob in the
// payload bytes, and attaches the header RVAs to the payload for
// writePECosmoAMD64. Runs on the thin amd64 path only (convertToAPE),
// where ctxt.loader is still alive.
func apePrepareNTBoot(ctxt *Link, p *apePayload) {
	ldr := ctxt.loader
	sym := func(name string, wantSize int64) uint64 {
		s := ldr.Lookup(name, 0)
		if s == 0 {
			Exitf("APE NT boot: symbol %s not found; it should be a deadcode root for cosmo/amd64", name)
		}
		if wantSize >= 0 && ldr.SymSize(s) != wantSize {
			Exitf("APE NT boot: %s is %d bytes, want %d (layout contract with rt0_cosmo_nt_amd64.s)", name, ldr.SymSize(s), wantSize)
		}
		v := uint64(ldr.SymValue(s))
		if v < peCosmoImageBase || v-peCosmoImageBase >= 1<<32 {
			Exitf("APE NT boot: %s at %#x is outside the PE image (base %#x)", name, v, uint64(peCosmoImageBase))
		}
		return v
	}
	entry := sym("_rt0_cosmo_nt", -1)
	idata := sym("runtime.ntidata", ntidataSize)
	iat := sym("runtime.ntiat", ntiatSize)

	loads := apePayloadLoads(p.elf)
	idataOff := apeVaddrFileOff(loads, idata, ntidataSize, "runtime.ntidata")
	// The IAT must be file-backed too: the NT loader resolves imports by
	// overwriting bytes that exist in the file image.
	apeVaddrFileOff(loads, iat, ntiatSize, "runtime.ntiat")

	// Cross-check the blob's fixed layout against the strings the asm
	// placed, so a drifted rt0_cosmo_nt_amd64.s fails the link loudly
	// instead of producing an unloadable import table.
	blob := p.elf[idataOff : idataOff+ntidataSize]
	for _, want := range []struct {
		off int
		s   string
	}{
		{ntidataHintGetProc + 2, "GetProcAddress\x00"},
		{ntidataHintLoadLib + 2, "LoadLibraryA\x00"},
		{ntidataDLLName, "kernel32.dll\x00"},
	} {
		if got := string(blob[want.off : want.off+len(want.s)]); got != want.s {
			Exitf("APE NT boot: runtime.ntidata+%#x holds %q, want %q; blob layout out of sync with rt0_cosmo_nt_amd64.s", want.off, got, want.s)
		}
	}

	idataRVA := uint32(idata - peCosmoImageBase)
	iatRVA := uint32(iat - peCosmoImageBase)
	// Patch the five RVA fields (layout comment in rt0_cosmo_nt_amd64.s).
	binary.LittleEndian.PutUint32(blob[0x00:], idataRVA+ntidataILT)                         // IDT[0].OriginalFirstThunk
	binary.LittleEndian.PutUint32(blob[0x0C:], idataRVA+ntidataDLLName)                     // IDT[0].Name
	binary.LittleEndian.PutUint32(blob[0x10:], iatRVA)                                      // IDT[0].FirstThunk
	binary.LittleEndian.PutUint64(blob[ntidataILT:], uint64(idataRVA)+ntidataHintGetProc)   // ILT[0]
	binary.LittleEndian.PutUint64(blob[ntidataILT+8:], uint64(idataRVA)+ntidataHintLoadLib) // ILT[1]

	p.pe = &apePEInfo{
		entryRVA:   uint32(entry - peCosmoImageBase),
		importsRVA: idataRVA,
	}
}

// peCosmoSection is one section header of the real amd64 PE header.
type peCosmoSection struct {
	name  string
	rva   uint32 // VirtualAddress
	vsz   uint32 // VirtualSize (BSS beyond rawsz is zero-filled)
	raw   uint32 // PointerToRawData, an absolute APE file offset
	rawsz uint32 // SizeOfRawData
	chars uint32
}

// writePECosmoAMD64 writes the real PE header for an amd64 payload: a
// PE32+ image at base peCosmoImageBase whose three sections map the
// payload's PT_LOADs (skipping the payload's ELF-header page, which the
// PE headers region occupies virtually), whose entry point is the
// runtime's _rt0_cosmo_nt stub, and whose import directory points at
// the runtime.ntidata blob patched by apePrepareNTBoot.
func writePECosmoAMD64(header []byte, amd *apePayload) {
	info := amd.pe
	loads := apePayloadLoads(amd.elf)
	if len(loads) != 3 {
		Exitf("APE PE: amd64 payload has %d PT_LOADs, want 3 (RX text, R rodata, RW data)", len(loads))
	}
	const (
		elfPFExec  = 1
		elfPFWrite = 2
		elfPFRead  = 4
	)
	wantFlags := [3]uint32{elfPFRead | elfPFExec, elfPFRead, elfPFRead | elfPFWrite}
	for i, l := range loads {
		if l.flags != wantFlags[i] {
			Exitf("APE PE: PT_LOAD %d has flags %#x, want %#x", i, l.flags, wantFlags[i])
		}
		if l.vaddr-l.off != peCosmoImageBase {
			Exitf("APE PE: PT_LOAD %d has vaddr %#x - offset %#x != image base %#x; RVAs would not equal payload offsets", i, l.vaddr, l.off, uint64(peCosmoImageBase))
		}
		if l.off%peCosmoSectAlign != 0 {
			Exitf("APE PE: PT_LOAD %d file offset %#x is not %#x-aligned", i, l.off, peCosmoSectAlign)
		}
		if l.memsz < l.filesz {
			Exitf("APE PE: PT_LOAD %d has memsz %#x < filesz %#x", i, l.memsz, l.filesz)
		}
	}
	text, ro, data := loads[0], loads[1], loads[2]
	if text.off != 0 || text.filesz <= peCosmoSectAlign {
		Exitf("APE PE: text load must start at payload offset 0 and extend past the ELF header page (off %#x, filesz %#x)", text.off, text.filesz)
	}
	if text.memsz != text.filesz {
		Exitf("APE PE: text load has memsz %#x != filesz %#x", text.memsz, text.filesz)
	}
	end := data.off + data.memsz
	if end >= 1<<32 {
		Exitf("APE PE: image end %#x does not fit the 32-bit RVA space", end)
	}

	// .data's SizeOfRawData is p_filesz rounded up to FileAlignment; the
	// rounding tail is loaded into memory ahead of the zero-filled BSS,
	// so it must be zero bytes in the file (the linker's next file area
	// starts at a page-rounded offset, leaving zero padding here).
	dataRawSize := (data.filesz + peCosmoFileAlign - 1) &^ uint64(peCosmoFileAlign-1)
	for i := data.off + data.filesz; i < data.off+dataRawSize; i++ {
		if i >= uint64(len(amd.elf)) || amd.elf[i] != 0 {
			Exitf("APE PE: byte %#x of the payload is not zero padding; cannot round .data raw size %#x up to FileAlignment", i, data.filesz)
		}
	}

	sects := [3]peCosmoSection{
		{".text", uint32(text.off + peCosmoSectAlign), uint32(text.memsz - peCosmoSectAlign),
			uint32(amd.offset+text.off) + peCosmoSectAlign, uint32(text.filesz - peCosmoSectAlign),
			0x60000020}, // CODE | EXECUTE | READ
		{".rodata", uint32(ro.off), uint32(ro.memsz),
			uint32(amd.offset + ro.off), uint32(ro.filesz),
			0x40000040}, // INITIALIZED_DATA | READ
		{".data", uint32(data.off), uint32(data.memsz),
			uint32(amd.offset + data.off), uint32(dataRawSize),
			0xC0000040}, // INITIALIZED_DATA | READ | WRITE
	}
	sizeOfImage := (uint32(end) + peCosmoSectAlign - 1) &^ uint32(peCosmoSectAlign-1)

	if t := sects[0]; info.entryRVA < t.rva || info.entryRVA >= t.rva+t.vsz {
		Exitf("APE PE: entry RVA %#x is outside .text [%#x, %#x)", info.entryRVA, t.rva, t.rva+t.vsz)
	}
	if d := sects[2]; info.importsRVA < d.rva || info.importsRVA+peCosmoImportsSize > d.rva+d.vsz {
		Exitf("APE PE: import directory RVA %#x is outside .data [%#x, %#x)", info.importsRVA, d.rva, d.rva+d.vsz)
	}

	peStart := 0x80
	copy(header[peStart:], []byte{'P', 'E', 0, 0})

	// COFF header.
	coffStart := peStart + 4
	binary.LittleEndian.PutUint16(header[coffStart+0:], 0x8664)          // Machine: amd64
	binary.LittleEndian.PutUint16(header[coffStart+2:], peCosmoSections) // NumberOfSections
	binary.LittleEndian.PutUint32(header[coffStart+4:], 0)               // TimeDateStamp
	binary.LittleEndian.PutUint32(header[coffStart+8:], 0)               // PointerToSymbolTable
	binary.LittleEndian.PutUint32(header[coffStart+12:], 0)              // NumberOfSymbols
	binary.LittleEndian.PutUint16(header[coffStart+16:], 240)            // SizeOfOptionalHeader
	// RELOCS_STRIPPED | EXECUTABLE_IMAGE | LARGE_ADDRESS_AWARE |
	// DEBUG_STRIPPED, matching real Cosmopolitan APEs. RELOCS_STRIPPED
	// is honest: cosmo code is position-dependent and there is no
	// .reloc section, so the image must load at ImageBase or not at all.
	binary.LittleEndian.PutUint16(header[coffStart+18:], 0x0223) // Characteristics

	// Optional header (PE32+).
	optStart := coffStart + 20
	binary.LittleEndian.PutUint16(header[optStart+0:], 0x20B)               // Magic: PE32+
	header[optStart+2] = 1                                                  // MajorLinkerVersion
	header[optStart+3] = 0                                                  // MinorLinkerVersion
	binary.LittleEndian.PutUint32(header[optStart+4:], 0)                   // SizeOfCode (unused by loaders)
	binary.LittleEndian.PutUint32(header[optStart+8:], 0)                   // SizeOfInitializedData
	binary.LittleEndian.PutUint32(header[optStart+12:], 0)                  // SizeOfUninitializedData
	binary.LittleEndian.PutUint32(header[optStart+16:], info.entryRVA)      // AddressOfEntryPoint
	binary.LittleEndian.PutUint32(header[optStart+20:], sects[0].rva)       // BaseOfCode
	binary.LittleEndian.PutUint64(header[optStart+24:], peCosmoImageBase)   // ImageBase
	binary.LittleEndian.PutUint32(header[optStart+32:], peCosmoSectAlign)   // SectionAlignment
	binary.LittleEndian.PutUint32(header[optStart+36:], peCosmoFileAlign)   // FileAlignment
	binary.LittleEndian.PutUint16(header[optStart+40:], 6)                  // MajorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+42:], 0)                  // MinorOSVersion
	binary.LittleEndian.PutUint16(header[optStart+44:], 0)                  // MajorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+46:], 0)                  // MinorImageVersion
	binary.LittleEndian.PutUint16(header[optStart+48:], 6)                  // MajorSubsystemVersion
	binary.LittleEndian.PutUint16(header[optStart+50:], 0)                  // MinorSubsystemVersion
	binary.LittleEndian.PutUint32(header[optStart+52:], 0)                  // Win32VersionValue
	binary.LittleEndian.PutUint32(header[optStart+56:], sizeOfImage)        // SizeOfImage
	binary.LittleEndian.PutUint32(header[optStart+60:], peCosmoHeadersSize) // SizeOfHeaders
	binary.LittleEndian.PutUint32(header[optStart+64:], 0)                  // CheckSum
	binary.LittleEndian.PutUint16(header[optStart+68:], 3)                  // Subsystem: CONSOLE
	// NX_COMPAT | TERMINAL_SERVER_AWARE. Deliberately no DYNAMIC_BASE or
	// HIGH_ENTROPY_VA: with relocations stripped, ASLR must not be
	// invited to move the image off its link base.
	binary.LittleEndian.PutUint16(header[optStart+70:], 0x8100)   // DllCharacteristics
	binary.LittleEndian.PutUint64(header[optStart+72:], 0x800000) // SizeOfStackReserve (8 MiB)
	// rt0_go carves g0's stack as [entry SP - 64K, entry SP], so the
	// commit must hand the entry thread at least 64K up front.
	binary.LittleEndian.PutUint64(header[optStart+80:], 0x10000)  // SizeOfStackCommit
	binary.LittleEndian.PutUint64(header[optStart+88:], 0x100000) // SizeOfHeapReserve
	binary.LittleEndian.PutUint64(header[optStart+96:], 0x1000)   // SizeOfHeapCommit
	binary.LittleEndian.PutUint32(header[optStart+104:], 0)       // LoaderFlags
	binary.LittleEndian.PutUint32(header[optStart+108:], 16)      // NumberOfRvaAndSizes
	// Data directories: only [1] (imports) is populated.
	dirStart := optStart + 112
	binary.LittleEndian.PutUint32(header[dirStart+8:], info.importsRVA)
	binary.LittleEndian.PutUint32(header[dirStart+12:], peCosmoImportsSize)

	// Section table (ends at 0x208, within the [0x80, 0x7FF) budget the
	// shell script at apeScriptOffset leaves for the PE header chain).
	sectStart := optStart + 240
	for i, s := range sects {
		sh := header[sectStart+40*i:]
		copy(sh[0:8], s.name)
		binary.LittleEndian.PutUint32(sh[8:], s.vsz)
		binary.LittleEndian.PutUint32(sh[12:], s.rva)
		binary.LittleEndian.PutUint32(sh[16:], s.rawsz)
		binary.LittleEndian.PutUint32(sh[20:], s.raw)
		binary.LittleEndian.PutUint32(sh[24:], 0) // PointerToRelocations
		binary.LittleEndian.PutUint32(sh[28:], 0) // PointerToLinenumbers
		binary.LittleEndian.PutUint16(sh[32:], 0) // NumberOfRelocations
		binary.LittleEndian.PutUint16(sh[34:], 0) // NumberOfLinenumbers
		binary.LittleEndian.PutUint32(sh[36:], s.chars)
	}
}

// transplantPEHeader copies the amd64 input's PE header region verbatim
// into a fat APE's head. The thin link computed a header whose RVAs and
// absolute raw data pointers are equally valid in the fat file: the
// amd64 image lands at the same file offset (apeHeaderSize) with
// byte-identical content, imports blob included.
func transplantPEHeader(header []byte, amd *apePayload) {
	if len(amd.head) < apeScriptOffset {
		Exitf("APE PE transplant: amd64 input head is %d bytes, want at least %#x", len(amd.head), apeScriptOffset)
	}
	if string(amd.head[0x80:0x84]) != "PE\x00\x00" {
		Exitf("APE PE transplant: amd64 input has no PE signature at 0x80")
	}
	if amd.offset != apeHeaderSize {
		Exitf("APE PE transplant: amd64 payload at %#x, want %#x; the transplanted header's raw data pointers assume the thin layout", amd.offset, uint64(apeHeaderSize))
	}
	copy(header[0x80:apeScriptOffset], amd.head[0x80:apeScriptOffset])
}

// writePEHeader writes the legacy stub PE header: a parseable console
// PE32+ whose entry immediately returns 0, mapping nothing of the
// payload. It remains for outputs that cannot carry the real header:
// arm64-only APEs (no NT support) and synthetic payloads without a live
// link or an input head (ld tests).
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
